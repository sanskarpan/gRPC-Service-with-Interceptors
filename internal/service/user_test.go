package service

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/internal/storage"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestUserLifecycleAndCopyIsolation(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	created, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "John Doe", Email: "john@example.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if created.Id == "" || created.CreatedAt == nil || created.UpdatedAt == nil {
		t.Fatalf("CreateUser() returned incomplete user: %#v", created)
	}
	created.Name = "mutated by caller"
	got, err := svc.GetUser(ctx, &pb.GetUserRequest{Id: created.Id})
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Name != "John Doe" || got.Email != "john@example.com" || got.Age != 30 {
		t.Fatalf("GetUser() returned unexpected user: %#v", got)
	}

	updated, err := svc.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.Id, Name: "Updated", Age: 31})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if updated.Name != "Updated" || updated.Age != 31 || updated.Email != "john@example.com" {
		t.Fatalf("UpdateUser() returned unexpected user: %#v", updated)
	}
	if _, err := svc.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.Id}); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	if _, err := svc.GetUser(ctx, &pb.GetUserRequest{Id: created.Id}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser() after delete code = %v, want NotFound", status.Code(err))
	}
}

func TestNewUserServiceNormalizesNonPositiveLimits(t *testing.T) {
	svc := NewUserService(ServiceLimits{})
	user, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{Name: "Defaulted", Email: "defaulted@example.com", Age: 1})
	if err != nil {
		t.Fatalf("CreateUser() with normalized limits: %v", err)
	}
	if user.GetId() == "" {
		t.Fatal("CreateUser() returned an empty id")
	}
}

func TestUserValidation(t *testing.T) {
	svc := NewUserService(testLimits())
	tests := []struct {
		name string
		req  *pb.CreateUserRequest
		code codes.Code
	}{
		{name: "nil request", req: nil, code: codes.InvalidArgument},
		{name: "missing name", req: &pb.CreateUserRequest{Email: "a@example.com"}, code: codes.InvalidArgument},
		{name: "whitespace name", req: &pb.CreateUserRequest{Name: "  ", Email: "a@example.com"}, code: codes.InvalidArgument},
		{name: "missing email", req: &pb.CreateUserRequest{Name: "A"}, code: codes.InvalidArgument},
		{name: "invalid email", req: &pb.CreateUserRequest{Name: "A", Email: "not-an-email"}, code: codes.InvalidArgument},
		{name: "negative age", req: &pb.CreateUserRequest{Name: "A", Email: "a@example.com", Age: -1}, code: codes.InvalidArgument},
		{name: "impossible age", req: &pb.CreateUserRequest{Name: "A", Email: "a@example.com", Age: 151}, code: codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateUser(context.Background(), tt.req)
			if got := status.Code(err); got != tt.code {
				t.Fatalf("CreateUser() code = %v, want %v (err=%v)", got, tt.code, err)
			}
		})
	}
}

func TestUserCapacity(t *testing.T) {
	svc := NewUserService(ServiceLimits{MaxUsers: 1, MaxStreamMessages: 2, MaxStringBytes: 64, StreamInterval: time.Millisecond})
	create := func(name string) {
		t.Helper()
		if _, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{Name: name, Email: name + "@example.com"}); err != nil {
			t.Fatalf("CreateUser() error = %v", err)
		}
	}
	create("first")
	if _, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{Name: "second", Email: "second@example.com"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("capacity error code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestUserConcurrentAccess(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	user, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "Concurrent", Email: "concurrent@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_, _ = svc.GetUser(ctx, &pb.GetUserRequest{Id: user.Id})
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = svc.UpdateUser(ctx, &pb.UpdateUserRequest{Id: user.Id, Name: "Name", Age: int32((i % 100) + 1)})
		}(i)
	}
	wg.Wait()
	got, err := svc.GetUser(ctx, &pb.GetUserRequest{Id: user.Id})
	if err != nil || got.Id != user.Id {
		t.Fatalf("final GetUser() = %#v, %v", got, err)
	}
}

func TestContextCancellation(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "A", Email: "a@example.com"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateUser() error = %v, want context.Canceled", err)
	}
}

func TestCollectUserMetrics(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeMetricsStream{
		ctx:  context.Background(),
		msgs: []*pb.UserMetric{{UserId: "user-1", MetricName: "latency", Value: 2}, {UserId: "user-1", MetricName: "latency", Value: 4}},
	}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	if err := svc.CollectUserMetrics(serverStream); err != nil {
		t.Fatalf("CollectUserMetrics() error = %v", err)
	}
	response, ok := stream.sent.(*pb.CollectMetricsResponse)
	if !ok || response.Count != 2 || response.Sum != 6 || response.Avg != 3 {
		t.Fatalf("CollectUserMetrics() response = %#v", stream.sent)
	}
}

func TestCollectUserMetricsRejectsTransportErrorAndZeroInput(t *testing.T) {
	svc := NewUserService(testLimits())
	transportErr := errors.New("transport failed")
	stream := &fakeMetricsStream{ctx: context.Background(), recvErr: transportErr}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	if err := svc.CollectUserMetrics(serverStream); !errors.Is(err, transportErr) {
		t.Fatalf("CollectUserMetrics() error = %v, want transport error", err)
	}

	empty := &fakeMetricsStream{ctx: context.Background(), done: true}
	emptyServerStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: empty}
	if err := svc.CollectUserMetrics(emptyServerStream); err != nil {
		t.Fatalf("empty CollectUserMetrics() error = %v", err)
	}
	response := empty.sent.(*pb.CollectMetricsResponse)
	if response.Count != 0 || response.Sum != 0 || response.Avg != 0 {
		t.Fatalf("empty response = %#v", response)
	}
}

func FuzzParseEmail(f *testing.F) {
	f.Add("user@example.com")
	f.Add("not-an-email")
	f.Fuzz(func(t *testing.T, value string) {
		_, _ = parseEmail(value)
	})
}

func BenchmarkGetUser(b *testing.B) {
	svc := NewUserService(ServiceLimits{MaxUsers: b.N + 1, MaxStreamMessages: 10, MaxStringBytes: 128, StreamInterval: time.Millisecond})
	user, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{Name: "Benchmark", Email: "benchmark@example.com"})
	if err != nil {
		b.Fatalf("CreateUser() error = %v", err)
	}
	req := &pb.GetUserRequest{Id: user.Id}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := svc.GetUser(context.Background(), req); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func testLimits() ServiceLimits {
	return ServiceLimits{MaxUsers: 1000, MaxStreamMessages: 5, MaxStringBytes: 64, StreamInterval: time.Millisecond}
}

type fakeMetricsStream struct {
	ctx     context.Context
	msgs    []*pb.UserMetric
	sent    any
	recvErr error
	done    bool
}

func (s *fakeMetricsStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeMetricsStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeMetricsStream) SetTrailer(metadata.MD)       {}
func (s *fakeMetricsStream) Context() context.Context     { return s.ctx }
func (s *fakeMetricsStream) SendMsg(message any) error    { s.sent = message; return nil }
func (s *fakeMetricsStream) RecvMsg(message any) error {
	if len(s.msgs) > 0 {
		metric := s.msgs[0]
		s.msgs = s.msgs[1:]
		proto.Reset(message.(*pb.UserMetric))
		proto.Merge(message.(*pb.UserMetric), metric)
		return nil
	}
	if s.recvErr != nil {
		return s.recvErr
	}
	if s.done || len(s.msgs) == 0 {
		return io.EOF
	}
	return nil
}

type fakeEventStream struct {
	ctx     context.Context
	sent    []*pb.UserEvent
	sendErr error
}

func (s *fakeEventStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeEventStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeEventStream) SetTrailer(metadata.MD)       {}
func (s *fakeEventStream) Context() context.Context     { return s.ctx }
func (s *fakeEventStream) SendMsg(m any) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, m.(*pb.UserEvent))
	return nil
}
func (s *fakeEventStream) RecvMsg(m any) error { return io.EOF }

type fakeChatStream struct {
	ctx     context.Context
	msgs    []*pb.ChatMessage
	sent    []*pb.ChatMessage
	recvErr error
	sendErr error
	index   int
}

func (s *fakeChatStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeChatStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeChatStream) SetTrailer(metadata.MD)       {}
func (s *fakeChatStream) Context() context.Context     { return s.ctx }
func (s *fakeChatStream) SendMsg(m any) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, m.(*pb.ChatMessage))
	return nil
}
func (s *fakeChatStream) RecvMsg(m any) error {
	if s.recvErr != nil {
		return s.recvErr
	}
	if s.index >= len(s.msgs) {
		return io.EOF
	}
	msg := s.msgs[s.index]
	s.index++
	proto.Reset(m.(*pb.ChatMessage))
	proto.Merge(m.(*pb.ChatMessage), msg)
	return nil
}

type mockRepository struct {
	getFn    func(ctx context.Context, id string) (*pb.User, error)
	createFn func(ctx context.Context, user *pb.User, maxUsers int) error
	updateFn func(ctx context.Context, user *pb.User) error
	deleteFn func(ctx context.Context, id string) error
	rmuFn    func(ctx context.Context, id string, mutate storage.ReadModifyUpdateFn) (*pb.User, error)
}

func (m *mockRepository) Ping(ctx context.Context) error { return nil }
func (m *mockRepository) Close() error                   { return nil }
func (m *mockRepository) Get(ctx context.Context, id string) (*pb.User, error) {
	return m.getFn(ctx, id)
}
func (m *mockRepository) Create(ctx context.Context, user *pb.User, maxUsers int) error {
	return m.createFn(ctx, user, maxUsers)
}
func (m *mockRepository) Update(ctx context.Context, user *pb.User) error {
	return m.updateFn(ctx, user)
}
func (m *mockRepository) Delete(ctx context.Context, id string) error {
	return m.deleteFn(ctx, id)
}
func (m *mockRepository) ReadModifyUpdate(ctx context.Context, id string, mutate storage.ReadModifyUpdateFn) (*pb.User, error) {
	if m.rmuFn != nil {
		return m.rmuFn(ctx, id, mutate)
	}
	current, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := mutate(current); err != nil {
		return nil, err
	}
	if err := m.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

type directMetricStream struct {
	ctx     context.Context
	msgs    []*pb.UserMetric
	sent    *pb.CollectMetricsResponse
	recvErr error
}

func (s *directMetricStream) Recv() (*pb.UserMetric, error) {
	if s.recvErr != nil {
		return nil, s.recvErr
	}
	if len(s.msgs) == 0 {
		return nil, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	return m, nil
}

func (s *directMetricStream) SendAndClose(res *pb.CollectMetricsResponse) error {
	s.sent = res
	return nil
}
func (s *directMetricStream) SetHeader(metadata.MD) error  { return nil }
func (s *directMetricStream) SendHeader(metadata.MD) error { return nil }
func (s *directMetricStream) SetTrailer(metadata.MD)       {}
func (s *directMetricStream) Context() context.Context     { return s.ctx }
func (s *directMetricStream) SendMsg(m any) error {
	if res, ok := m.(*pb.CollectMetricsResponse); ok {
		s.sent = res
	}
	return nil
}
func (s *directMetricStream) RecvMsg(m any) error { return io.EOF }

func TestGetUserNilRequest(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.GetUser(context.Background(), nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetUser(nil) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestGetUserEmptyID(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.GetUser(context.Background(), &pb.GetUserRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetUser(empty) code = %v, want InvalidArgument", status.Code(err))
	}
	_, err = svc.GetUser(context.Background(), &pb.GetUserRequest{Id: "  "})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetUser(whitespace) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestGetUserNotFound(t *testing.T) {
	svc := NewUserServiceWithRepository(&mockRepository{
		getFn: func(_ context.Context, _ string) (*pb.User, error) {
			return nil, storage.ErrNotFound
		},
	}, testLimits())
	_, err := svc.GetUser(context.Background(), &pb.GetUserRequest{Id: "nonexistent"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser(not found) code = %v, want NotFound (err=%v)", status.Code(err), err)
	}
}

func TestCreateUserNilRepository(t *testing.T) {
	svc := NewUserServiceWithRepository(nil, testLimits())
	_, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("CreateUser(nil repo) code = %v, want Unavailable", status.Code(err))
	}
}

func TestUpdateUserHappyPath(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	created, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "Original", Email: "original@example.com", Age: 25})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	originalUpdatedAt := created.UpdatedAt

	updated, err := svc.UpdateUser(ctx, &pb.UpdateUserRequest{
		Id:    created.Id,
		Name:  "UpdatedName",
		Email: "updated@example.com",
		Age:   30,
	})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if updated.Name != "UpdatedName" {
		t.Fatalf("Name = %q, want UpdatedName", updated.Name)
	}
	if updated.Email != "updated@example.com" {
		t.Fatalf("Email = %q, want updated@example.com", updated.Email)
	}
	if updated.Age != 30 {
		t.Fatalf("Age = %d, want 30", updated.Age)
	}
	if updated.Id != created.Id {
		t.Fatalf("Id changed from %q to %q", created.Id, updated.Id)
	}
	if updated.UpdatedAt.AsTime().Before(originalUpdatedAt.AsTime()) {
		t.Fatal("UpdatedAt should be after original UpdatedAt")
	}
}

func TestUpdateUserNilRequest(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.UpdateUser(context.Background(), nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateUser(nil) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestUpdateUserEmptyID(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.UpdateUser(context.Background(), &pb.UpdateUserRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateUser(empty id) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestUpdateUserNotFound(t *testing.T) {
	svc := NewUserServiceWithRepository(&mockRepository{
		getFn: func(_ context.Context, _ string) (*pb.User, error) {
			return nil, storage.ErrNotFound
		},
	}, testLimits())
	_, err := svc.UpdateUser(context.Background(), &pb.UpdateUserRequest{Id: "nonexistent", Name: "test"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("UpdateUser(not found) code = %v, want NotFound", status.Code(err))
	}
}

func TestUpdateUserInvalidAge(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	created, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	tests := []struct {
		name string
		age  int32
	}{
		{name: "negative age", age: -1},
		{name: "age over 150", age: 151},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.Id, Age: tt.age})
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("UpdateUser(age=%d) code = %v, want InvalidArgument", tt.age, status.Code(err))
			}
		})
	}
}

func TestUpdateUserInvalidEmail(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	created, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	_, err = svc.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.Id, Email: "not-an-email"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateUser(bad email) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestDeleteUserHappyPath(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	created, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "ToDelete", Email: "delete@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	_, err = svc.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.Id})
	if err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	_, err = svc.GetUser(ctx, &pb.GetUserRequest{Id: created.Id})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser after delete code = %v, want NotFound", status.Code(err))
	}
}

func TestDeleteUserNilRequest(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.DeleteUser(context.Background(), nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("DeleteUser(nil) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestDeleteUserEmptyID(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.DeleteUser(context.Background(), &pb.DeleteUserRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("DeleteUser(empty id) code = %v, want InvalidArgument", status.Code(err))
	}
	_, err = svc.DeleteUser(context.Background(), &pb.DeleteUserRequest{Id: "  "})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("DeleteUser(whitespace id) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestDeleteUserNotFound(t *testing.T) {
	svc := NewUserServiceWithRepository(&mockRepository{
		deleteFn: func(_ context.Context, _ string) error {
			return storage.ErrNotFound
		},
	}, testLimits())
	_, err := svc.DeleteUser(context.Background(), &pb.DeleteUserRequest{Id: "nonexistent"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("DeleteUser(not found) code = %v, want NotFound", status.Code(err))
	}
}

func TestStreamUserEventsHappyPath(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	user, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "Stream", Email: "stream@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	stream := &fakeEventStream{ctx: ctx}
	serverStream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: stream}

	err = svc.StreamUserEvents(&pb.StreamUserEventsRequest{
		UserId:     user.Id,
		EventTypes: []string{"login", "logout"},
	}, serverStream)
	if err != nil {
		t.Fatalf("StreamUserEvents() error = %v", err)
	}
	if len(stream.sent) == 0 {
		t.Fatal("StreamUserEvents() sent no events")
	}
	for i, event := range stream.sent {
		if event.UserId != user.Id {
			t.Fatalf("event[%d].UserId = %q, want %q", i, event.UserId, user.Id)
		}
		if event.Timestamp == nil {
			t.Fatalf("event[%d].Timestamp is nil", i)
		}
	}
}

func TestStreamUserEventsNilRequest(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: &fakeEventStream{ctx: context.Background()}}
	err := svc.StreamUserEvents(nil, stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("StreamUserEvents(nil) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestStreamUserEventsEmptyUserID(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: &fakeEventStream{ctx: context.Background()}}
	err := svc.StreamUserEvents(&pb.StreamUserEventsRequest{UserId: ""}, stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("StreamUserEvents(empty user_id) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestStreamUserEventsTooManyEventTypes(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: &fakeEventStream{ctx: context.Background()}}
	err := svc.StreamUserEvents(&pb.StreamUserEventsRequest{
		UserId:     "user-1",
		EventTypes: []string{"a", "b", "c", "d", "e", "f"},
	}, stream)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("StreamUserEvents(too many) code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestStreamUserEventsUserNotFound(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: &fakeEventStream{ctx: context.Background()}}
	err := svc.StreamUserEvents(&pb.StreamUserEventsRequest{UserId: "nonexistent"}, stream)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("StreamUserEvents(not found) code = %v, want NotFound", status.Code(err))
	}
}

func TestStreamUserEventsContextCancelled(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx, cancel := context.WithCancel(context.Background())
	user, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "CancelTest", Email: "cancel@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	stream := &fakeEventStream{ctx: ctx}
	serverStream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: stream}

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.StreamUserEvents(&pb.StreamUserEventsRequest{UserId: user.Id}, serverStream)
	}()
	time.Sleep(3 * time.Millisecond)
	cancel()

	err = <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StreamUserEvents(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestCollectUserMetricsNilMetric(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &directMetricStream{ctx: context.Background(), msgs: []*pb.UserMetric{nil}}
	err := svc.CollectUserMetrics(stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CollectUserMetrics(nil metric) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCollectUserMetricsNaNValue(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeMetricsStream{
		ctx:  context.Background(),
		msgs: []*pb.UserMetric{{UserId: "user-1", MetricName: "latency", Value: math.NaN()}},
	}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	err := svc.CollectUserMetrics(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CollectUserMetrics(NaN) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCollectUserMetricsInfValue(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeMetricsStream{
		ctx:  context.Background(),
		msgs: []*pb.UserMetric{{UserId: "user-1", MetricName: "latency", Value: math.Inf(1)}},
	}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	err := svc.CollectUserMetrics(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CollectUserMetrics(+Inf) code = %v, want InvalidArgument", status.Code(err))
	}

	streamNeg := &fakeMetricsStream{
		ctx:  context.Background(),
		msgs: []*pb.UserMetric{{UserId: "user-1", MetricName: "latency", Value: math.Inf(-1)}},
	}
	serverStreamNeg := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: streamNeg}
	err = svc.CollectUserMetrics(serverStreamNeg)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CollectUserMetrics(-Inf) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCollectUserMetricsOutOfRangeSum(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeMetricsStream{
		ctx: context.Background(),
		msgs: []*pb.UserMetric{
			{UserId: "user-1", MetricName: "latency", Value: math.MaxFloat64},
			{UserId: "user-1", MetricName: "latency", Value: math.MaxFloat64},
		},
	}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	err := svc.CollectUserMetrics(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CollectUserMetrics(overflow) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCollectUserMetricsStreamLimitExceeded(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeMetricsStream{
		ctx: context.Background(),
		msgs: []*pb.UserMetric{
			{UserId: "user-1", MetricName: "m", Value: 1},
			{UserId: "user-1", MetricName: "m", Value: 2},
			{UserId: "user-1", MetricName: "m", Value: 3},
			{UserId: "user-1", MetricName: "m", Value: 4},
			{UserId: "user-1", MetricName: "m", Value: 5},
			{UserId: "user-1", MetricName: "m", Value: 6},
		},
	}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	err := svc.CollectUserMetrics(serverStream)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("CollectUserMetrics(limit) code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestCollectUserMetricsEmptyUserID(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeMetricsStream{
		ctx:  context.Background(),
		msgs: []*pb.UserMetric{{UserId: "", MetricName: "m", Value: 1}},
	}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	err := svc.CollectUserMetrics(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CollectUserMetrics(empty user_id) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCollectUserMetricsEmptyMetricName(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeMetricsStream{
		ctx:  context.Background(),
		msgs: []*pb.UserMetric{{UserId: "user-1", MetricName: "", Value: 1}},
	}
	serverStream := &grpc.GenericServerStream[pb.UserMetric, pb.CollectMetricsResponse]{ServerStream: stream}
	err := svc.CollectUserMetrics(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CollectUserMetrics(empty metric_name) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestChatStreamHappyPath(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	user, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "Chatter", Email: "chat@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	stream := &fakeChatStream{
		ctx: ctx,
		msgs: []*pb.ChatMessage{
			{UserId: user.Id, Message: "hello"},
			{UserId: user.Id, Message: "world"},
		},
	}
	serverStream := &grpc.GenericServerStream[pb.ChatMessage, pb.ChatMessage]{ServerStream: stream}
	err = svc.ChatStream(serverStream)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if len(stream.sent) != 2 {
		t.Fatalf("ChatStream sent %d replies, want 2", len(stream.sent))
	}
	for i, reply := range stream.sent {
		if reply.Id == "" {
			t.Fatalf("reply[%d].Id is empty", i)
		}
		if !strings.HasPrefix(reply.Id, "msg-") {
			t.Fatalf("reply[%d].Id = %q, want msg- prefix", i, reply.Id)
		}
		if reply.UserId != user.Id {
			t.Fatalf("reply[%d].UserId = %q, want %q", i, reply.UserId, user.Id)
		}
		if reply.Timestamp == nil {
			t.Fatalf("reply[%d].Timestamp is nil", i)
		}
	}
	if stream.sent[0].Message != "hello" || stream.sent[1].Message != "world" {
		t.Fatalf("reply messages mismatch: got %q and %q", stream.sent[0].Message, stream.sent[1].Message)
	}
}

func TestChatStreamNilMessage(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeChatStream{
		ctx:  context.Background(),
		msgs: []*pb.ChatMessage{nil},
	}
	serverStream := &grpc.GenericServerStream[pb.ChatMessage, pb.ChatMessage]{ServerStream: stream}
	err := svc.ChatStream(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ChatStream(nil msg) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestChatStreamEmptyMessage(t *testing.T) {
	svc := NewUserService(testLimits())
	user, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	stream := &fakeChatStream{
		ctx:  context.Background(),
		msgs: []*pb.ChatMessage{{UserId: user.Id, Message: ""}},
	}
	serverStream := &grpc.GenericServerStream[pb.ChatMessage, pb.ChatMessage]{ServerStream: stream}
	err = svc.ChatStream(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ChatStream(empty msg) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestChatStreamInvalidUserID(t *testing.T) {
	svc := NewUserService(testLimits())
	stream := &fakeChatStream{
		ctx:  context.Background(),
		msgs: []*pb.ChatMessage{{UserId: "", Message: "hello"}},
	}
	serverStream := &grpc.GenericServerStream[pb.ChatMessage, pb.ChatMessage]{ServerStream: stream}
	err := svc.ChatStream(serverStream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ChatStream(invalid user_id) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestChatStreamRecvTransportError(t *testing.T) {
	svc := NewUserService(testLimits())
	transportErr := errors.New("recv transport failure")
	stream := &fakeChatStream{ctx: context.Background(), recvErr: transportErr}
	serverStream := &grpc.GenericServerStream[pb.ChatMessage, pb.ChatMessage]{ServerStream: stream}
	err := svc.ChatStream(serverStream)
	if !errors.Is(err, transportErr) {
		t.Fatalf("ChatStream(recv err) = %v, want transport error", err)
	}
}

func TestChatStreamSendError(t *testing.T) {
	svc := NewUserService(testLimits())
	user, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	sendErr := errors.New("send transport failure")
	stream := &fakeChatStream{
		ctx:     context.Background(),
		msgs:    []*pb.ChatMessage{{UserId: user.Id, Message: "hello"}},
		sendErr: sendErr,
	}
	serverStream := &grpc.GenericServerStream[pb.ChatMessage, pb.ChatMessage]{ServerStream: stream}
	err = svc.ChatStream(serverStream)
	if !errors.Is(err, sendErr) {
		t.Fatalf("ChatStream(send err) = %v, want send error", err)
	}
}

func TestNewID(t *testing.T) {
	id, err := newID("test")
	if err != nil {
		t.Fatalf("newID() error = %v", err)
	}
	if !strings.HasPrefix(id, "test-") {
		t.Fatalf("newID() = %q, want 'test-' prefix", id)
	}
	hexPart := id[5:]
	if len(hexPart) != 32 {
		t.Fatalf("hex part length = %d, want 32 (id=%q)", len(hexPart), id)
	}
}

func TestParseEmail(t *testing.T) {
	t.Run("valid email", func(t *testing.T) {
		parsed, err := parseEmail("user@example.com")
		if err != nil {
			t.Fatalf("parseEmail(valid) error = %v", err)
		}
		if parsed != "user@example.com" {
			t.Fatalf("parseEmail() = %q, want user@example.com", parsed)
		}
	})

	t.Run("invalid email", func(t *testing.T) {
		_, err := parseEmail("not-an-email")
		if err == nil {
			t.Fatal("parseEmail(invalid) expected error")
		}
	})

	t.Run("control characters", func(t *testing.T) {
		_, err := parseEmail("user@example.com\r\n")
		if err == nil {
			t.Fatal("parseEmail(control chars) expected error")
		}
	})
}

func TestValidateText(t *testing.T) {
	svc := NewUserService(testLimits())

	t.Run("empty", func(t *testing.T) {
		if err := svc.validateText("", "field"); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("validateText('') code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("too long", func(t *testing.T) {
		long := strings.Repeat("a", 65)
		if err := svc.validateText(long, "field"); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("validateText(long) code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("whitespace only", func(t *testing.T) {
		if err := svc.validateText("   ", "field"); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("validateText('   ') code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("valid", func(t *testing.T) {
		if err := svc.validateText("valid", "field"); err != nil {
			t.Fatalf("validateText('valid') error = %v", err)
		}
	})
}

func TestValidateContextNil(t *testing.T) {
	err := validateContext(nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("validateContext(nil) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestValidateContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := validateContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validateContext(canceled) error = %v, want context.Canceled", err)
	}
}

func TestGetUserContextCancelled(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.GetUser(ctx, &pb.GetUserRequest{Id: "test"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetUser(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestUpdateUserContextCancelled(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.UpdateUser(ctx, &pb.UpdateUserRequest{Id: "test"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("UpdateUser(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestDeleteUserContextCancelled(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.DeleteUser(ctx, &pb.DeleteUserRequest{Id: "test"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteUser(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestGetUserNilRepository(t *testing.T) {
	svc := NewUserServiceWithRepository(nil, testLimits())
	_, err := svc.GetUser(context.Background(), &pb.GetUserRequest{Id: "test"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("GetUser(nil repo) code = %v, want Unavailable", status.Code(err))
	}
}

func TestUpdateUserNilRepository(t *testing.T) {
	svc := NewUserServiceWithRepository(nil, testLimits())
	_, err := svc.UpdateUser(context.Background(), &pb.UpdateUserRequest{Id: "test"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("UpdateUser(nil repo) code = %v, want Unavailable", status.Code(err))
	}
}

func TestDeleteUserNilRepository(t *testing.T) {
	svc := NewUserServiceWithRepository(nil, testLimits())
	_, err := svc.DeleteUser(context.Background(), &pb.DeleteUserRequest{Id: "test"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("DeleteUser(nil repo) code = %v, want Unavailable", status.Code(err))
	}
}

func TestUpdateUserNameTooLong(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	created, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	_, err = svc.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.Id, Name: strings.Repeat("a", 65)})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateUser(long name) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestUpdateUserEmailTooLong(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	created, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	_, err = svc.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.Id, Email: strings.Repeat("a", 60) + "@b.com"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateUser(long email) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCreateUserNameTooLong(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{
		Name: strings.Repeat("a", 65), Email: "test@example.com",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser(long name) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestCreateUserEmailTooLong(t *testing.T) {
	svc := NewUserService(testLimits())
	_, err := svc.CreateUser(context.Background(), &pb.CreateUserRequest{
		Name: "test", Email: strings.Repeat("a", 60) + "@b.com",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser(long email) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestStreamUserEventsEmptyEventType(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	user, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	stream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: &fakeEventStream{ctx: ctx}}
	err = svc.StreamUserEvents(&pb.StreamUserEventsRequest{
		UserId:     user.Id,
		EventTypes: []string{""},
	}, stream)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("StreamUserEvents(empty event_type) code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestStreamUserEventsSendError(t *testing.T) {
	svc := NewUserService(testLimits())
	ctx := context.Background()
	user, err := svc.CreateUser(ctx, &pb.CreateUserRequest{Name: "test", Email: "test@example.com"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	sendErr := errors.New("send failure")
	stream := &fakeEventStream{ctx: ctx, sendErr: sendErr}
	serverStream := &grpc.GenericServerStream[pb.StreamUserEventsRequest, pb.UserEvent]{ServerStream: stream}
	err = svc.StreamUserEvents(&pb.StreamUserEventsRequest{UserId: user.Id}, serverStream)
	if !errors.Is(err, sendErr) {
		t.Fatalf("StreamUserEvents(send err) = %v, want send failure", err)
	}
}

func TestNewUserServiceWithRepositoryNormalizesLimits(t *testing.T) {
	svc := NewUserServiceWithRepository(storage.NewMemoryRepository(), ServiceLimits{})
	if svc.maxUsers != 10000 {
		t.Fatalf("maxUsers = %d, want 10000", svc.maxUsers)
	}
	if svc.maxStreamMessages != 100 {
		t.Fatalf("maxStreamMessages = %d, want 100", svc.maxStreamMessages)
	}
	if svc.maxStringBytes != 4096 {
		t.Fatalf("maxStringBytes = %d, want 4096", svc.maxStringBytes)
	}
}

func TestMapStorageError(t *testing.T) {
	tests := []struct {
		name          string
		input         error
		wantCode      codes.Code
		wantMsg       string
		wantErrorsIs  error
	}{
		{name: "context canceled", input: context.Canceled, wantCode: codes.Canceled, wantMsg: "context canceled", wantErrorsIs: context.Canceled},
		{name: "deadline exceeded", input: context.DeadlineExceeded, wantCode: codes.DeadlineExceeded, wantMsg: "context deadline exceeded", wantErrorsIs: context.DeadlineExceeded},
		{name: "not found", input: storage.ErrNotFound, wantCode: codes.NotFound, wantMsg: "user not found"},
		{name: "capacity", input: storage.ErrCapacity, wantCode: codes.ResourceExhausted, wantMsg: "user capacity reached"},
		{name: "already exists", input: storage.ErrAlreadyExists, wantCode: codes.AlreadyExists, wantMsg: "user already exists"},
		{name: "invalid", input: storage.ErrInvalid, wantCode: codes.InvalidArgument, wantMsg: "user data is invalid"},
		{name: "unknown error", input: errors.New("random"), wantCode: codes.Unavailable, wantMsg: "storage temporarily unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapStorageError(tt.input)
			if tt.wantErrorsIs != nil {
				if !errors.Is(err, tt.wantErrorsIs) {
					t.Fatalf("mapStorageError(%v) errors.Is = false, want true", tt.input)
				}
			} else {
				if got := status.Code(err); got != tt.wantCode {
					t.Fatalf("mapStorageError(%v) code = %v, want %v", tt.input, got, tt.wantCode)
				}
				if err.Error() != tt.wantMsg {
					t.Fatalf("mapStorageError(%v) msg = %q, want %q", tt.input, err.Error(), tt.wantMsg)
				}
			}
		})
	}
}
