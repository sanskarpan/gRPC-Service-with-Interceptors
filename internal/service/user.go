package service

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/mail"
	"strings"
	"time"

	"github.com/example/grpc-service/internal/storage"
	appErrors "github.com/example/grpc-service/pkg/errors"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ServiceLimits bounds mutable state and streaming work performed by the demo
// service. The defaults are intentionally finite so a bad client cannot grow
// process memory without limit.
type ServiceLimits struct {
	MaxUsers          int
	MaxStreamMessages int
	MaxStringBytes    int
	StreamInterval    time.Duration
}

// UserService implements the transport-facing contract over an injected,
// bounded repository. Memory is suitable for tests; production wiring injects
// the PostgreSQL implementation.
type UserService struct {
	pb.UnimplementedUserServiceServer
	repository        storage.Repository
	maxUsers          int
	maxStreamMessages int
	maxStringBytes    int
	streamInterval    time.Duration
}

// NewUserService creates a bounded service. An optional limits value is useful
// for tests; production callers should pass limits from validated config.
func NewUserService(optional ...ServiceLimits) *UserService {
	limits := ServiceLimits{
		MaxUsers:          10000,
		MaxStreamMessages: 100,
		MaxStringBytes:    4096,
		StreamInterval:    2 * time.Second,
	}
	if len(optional) > 0 {
		limits = optional[0]
	}
	if limits.MaxUsers < 1 {
		limits.MaxUsers = 10000
	}
	if limits.MaxStreamMessages < 1 {
		limits.MaxStreamMessages = 100
	}
	if limits.MaxStringBytes < 1 {
		limits.MaxStringBytes = 4096
	}
	if limits.StreamInterval <= 0 {
		limits.StreamInterval = 2 * time.Second
	}
	return NewUserServiceWithRepository(storage.NewMemoryRepository(), limits)
}

// NewUserServiceWithRepository constructs a service over an injected
// repository. The repository remains owned by the caller and must be closed by
// that owner during process shutdown.
func NewUserServiceWithRepository(repository storage.Repository, limits ServiceLimits) *UserService {
	if limits.MaxUsers < 1 {
		limits.MaxUsers = 10000
	}
	if limits.MaxStreamMessages < 1 {
		limits.MaxStreamMessages = 100
	}
	if limits.MaxStringBytes < 1 {
		limits.MaxStringBytes = 4096
	}
	if limits.StreamInterval <= 0 {
		limits.StreamInterval = 2 * time.Second
	}
	return &UserService{
		repository:        repository,
		maxUsers:          limits.MaxUsers,
		maxStreamMessages: limits.MaxStreamMessages,
		maxStringBytes:    limits.MaxStringBytes,
		streamInterval:    limits.StreamInterval,
	}
}

// GetUser returns a copy so callers cannot mutate shared service state.
func (s *UserService) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.Id) == "" {
		return nil, appErrors.ErrInvalidArgument("id is required")
	}
	if s.repository == nil {
		return nil, mapStorageError(storage.ErrUnavailable)
	}

	user, err := s.repository.Get(ctx, req.Id)
	if err != nil {
		return nil, mapStorageError(err)
	}
	return user, nil
}

// CreateUser validates and stores a user until the bounded in-memory capacity
// is reached. It is not idempotent because the protobuf contract has no key.
func (s *UserService) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.User, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, appErrors.ErrInvalidArgument("request is required")
	}
	name, email, err := validateUserFields(req.Name, req.Email, int(req.Age), s.maxStringBytes)
	if err != nil {
		return nil, err
	}

	id, err := newID("user")
	if err != nil {
		return nil, appErrors.ErrInternal("could not allocate user id")
	}
	now := timestamppb.Now()
	user := &pb.User{
		Id:        id,
		Name:      name,
		Email:     email,
		Age:       req.Age,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if s.repository == nil {
		return nil, mapStorageError(storage.ErrUnavailable)
	}
	if err := s.repository.Create(ctx, user, s.maxUsers); err != nil {
		return nil, mapStorageError(err)
	}
	return proto.Clone(user).(*pb.User), nil
}

// UpdateUser applies the non-zero fields in the request and returns a copy.
// ReadModifyUpdate prevents a lost-write race between Get and Update when two
// callers update the same row concurrently. The current proto contract
// cannot distinguish an omitted age from age zero.
func (s *UserService) UpdateUser(ctx context.Context, req *pb.UpdateUserRequest) (*pb.User, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.Id) == "" {
		return nil, appErrors.ErrInvalidArgument("id is required")
	}
	if s.repository == nil {
		return nil, mapStorageError(storage.ErrUnavailable)
	}
	if err := validateOptionalText(req.Name, s.maxStringBytes, "name"); err != nil {
		return nil, err
	}
	if err := validateOptionalText(req.Email, s.maxStringBytes, "email"); err != nil {
		return nil, err
	}
	if req.Email != "" {
		if _, err := parseEmail(req.Email); err != nil {
			return nil, appErrors.ErrInvalidArgument("email is invalid")
		}
	}
	if req.Age < 0 || req.Age > 150 {
		return nil, appErrors.ErrInvalidArgument("age must be between 0 and 150")
	}

	updated, err := s.repository.ReadModifyUpdate(ctx, req.Id, func(current *pb.User) error {
		if req.Name != "" {
			current.Name = strings.TrimSpace(req.Name)
		}
		if req.Email != "" {
			current.Email = strings.TrimSpace(req.Email)
		}
		if req.Age != 0 {
			current.Age = req.Age
		}
		current.UpdatedAt = timestamppb.Now()
		return nil
	})
	if err != nil {
		return nil, mapStorageError(err)
	}
	return proto.Clone(updated).(*pb.User), nil
}

// DeleteUser removes a user. Retrying a delete after success returns NotFound;
// callers that need idempotent writes must add an idempotency key to the API.
func (s *UserService) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*emptypb.Empty, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.Id) == "" {
		return nil, appErrors.ErrInvalidArgument("id is required")
	}
	if s.repository == nil {
		return nil, mapStorageError(storage.ErrUnavailable)
	}

	if err := s.repository.Delete(ctx, req.Id); err != nil {
		return nil, mapStorageError(err)
	}
	return &emptypb.Empty{}, nil
}

// StreamUserEvents emits bounded, cancellable events for an existing user.
func (s *UserService) StreamUserEvents(req *pb.StreamUserEventsRequest, stream pb.UserService_StreamUserEventsServer) error {
	if req == nil || strings.TrimSpace(req.UserId) == "" {
		return appErrors.ErrInvalidArgument("user_id is required")
	}
	if err := s.validateText(req.UserId, "user_id"); err != nil {
		return err
	}
	if len(req.EventTypes) > s.maxStreamMessages {
		return appErrors.ErrResourceExhausted("too many event types")
	}
	for _, eventType := range req.EventTypes {
		if err := s.validateText(eventType, "event_type"); err != nil {
			return err
		}
	}
	if _, err := s.GetUser(stream.Context(), &pb.GetUserRequest{Id: req.UserId}); err != nil {
		return err
	}

	ticker := time.NewTicker(s.streamInterval)
	defer ticker.Stop()
	for i := 0; i < s.maxStreamMessages; i++ {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			eventType := fmt.Sprintf("event_%d", i)
			if len(req.EventTypes) > 0 {
				eventType = req.EventTypes[i%len(req.EventTypes)]
			}
			event := &pb.UserEvent{
				UserId:    req.UserId,
				EventType: eventType,
				Payload:   fmt.Sprintf("{\"data\":%d}", i),
				Timestamp: timestamppb.Now(),
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
	return nil
}

// CollectUserMetrics validates and summarizes a bounded client stream.
func (s *UserService) CollectUserMetrics(stream pb.UserService_CollectUserMetricsServer) error {
	var sum float64
	var count int32
	for {
		metric, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if metric == nil {
			return appErrors.ErrInvalidArgument("metric is required")
		}
		if count >= int32(s.maxStreamMessages) {
			return appErrors.ErrResourceExhausted("metric stream limit reached")
		}
		if err := s.validateText(metric.UserId, "user_id"); err != nil {
			return err
		}
		if err := s.validateText(metric.MetricName, "metric_name"); err != nil {
			return err
		}
		if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
			return appErrors.ErrInvalidArgument("metric value must be finite")
		}
		sum += metric.Value
		if math.IsInf(sum, 0) {
			return appErrors.ErrInvalidArgument("metric sum is out of range")
		}
		count++
	}
	average := 0.0
	if count > 0 {
		average = sum / float64(count)
	}
	return stream.SendAndClose(&pb.CollectMetricsResponse{Count: count, Sum: sum, Avg: average})
}

// ChatStream echoes bounded messages while honoring cancellation and transport
// backpressure. Incoming protobuf messages are cloned before modification.
func (s *UserService) ChatStream(stream pb.UserService_ChatStreamServer) error {
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if msg == nil {
			return appErrors.ErrInvalidArgument("message is required")
		}
		if err := s.validateText(msg.UserId, "user_id"); err != nil {
			return err
		}
		if err := s.validateText(msg.Message, "message"); err != nil {
			return err
		}
		reply := proto.Clone(msg).(*pb.ChatMessage)
		reply.Timestamp = timestamppb.Now()
		id, err := newID("msg")
		if err != nil {
			return appErrors.ErrInternal("could not allocate message id")
		}
		reply.Id = id
		if err := stream.Send(reply); err != nil {
			return err
		}
	}
}

func (s *UserService) validateText(value, field string) error {
	if len(value) == 0 || len(value) > s.maxStringBytes || strings.TrimSpace(value) == "" {
		return appErrors.ErrInvalidArgument(field + " is required and must be within the configured size limit")
	}
	return nil
}

func validateUserFields(name, email string, age, maxBytes int) (string, string, error) {
	if err := validateOptionalText(name, maxBytes, "name"); err != nil || strings.TrimSpace(name) == "" {
		return "", "", appErrors.ErrInvalidArgument("name is required")
	}
	if err := validateOptionalText(email, maxBytes, "email"); err != nil {
		return "", "", err
	}
	normalizedEmail, err := parseEmail(email)
	if err != nil {
		return "", "", appErrors.ErrInvalidArgument("email is invalid")
	}
	if age < 0 || age > 150 {
		return "", "", appErrors.ErrInvalidArgument("age must be between 0 and 150")
	}
	return strings.TrimSpace(name), normalizedEmail, nil
}

func validateOptionalText(value string, maxBytes int, field string) error {
	if value != "" && (len(value) > maxBytes || strings.TrimSpace(value) == "") {
		return appErrors.ErrInvalidArgument(field + " is invalid")
	}
	return nil
}

func parseEmail(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("email contains control characters")
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || !strings.Contains(address.Address, "@") {
		return "", fmt.Errorf("invalid email")
	}
	return strings.TrimSpace(value), nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return appErrors.ErrInvalidArgument("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func newID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := crand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(bytes[:]), nil
}

func mapStorageError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, storage.ErrNotFound):
		return appErrors.ErrNotFound("user not found")
	case errors.Is(err, storage.ErrCapacity):
		return appErrors.ErrResourceExhausted("user capacity reached")
	case errors.Is(err, storage.ErrAlreadyExists):
		return appErrors.ErrAlreadyExists("user already exists")
	case errors.Is(err, storage.ErrInvalid):
		return appErrors.ErrInvalidArgument("user data is invalid")
	default:
		return appErrors.ErrUnavailable("storage temporarily unavailable")
	}
}
