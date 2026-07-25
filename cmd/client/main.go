package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/interceptors"
	"github.com/example/grpc-service/pkg/logging"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if strings.TrimSpace(configPath) == "" {
		configPath = "configs/config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	logging.Init(cfg.Logging.Level, cfg.Logging.Format)

	operations := []func(*config.Config) error{
		runUnaryClient,
		runServerStreamingClient,
		runClientStreamingClient,
		runBidiStreamingClient,
	}
	for _, operation := range operations {
		if err := operation(cfg); err != nil {
			logging.Logger().Error().Err(err).Msg("client operation failed")
		}
	}
}

func dialClient(cfg *config.Config) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(
			interceptors.UnaryClientLoggingInterceptor,
			interceptors.UnaryClientMetricsInterceptor,
			interceptors.UnaryClientAuthInterceptorWithToken(cfg.Client.AuthToken),
		),
		grpc.WithChainStreamInterceptor(
			interceptors.StreamClientLoggingInterceptor,
			interceptors.StreamClientMetricsInterceptor,
			interceptors.StreamClientAuthInterceptorWithToken(cfg.Client.AuthToken),
		),
	}
	if cfg.TLS.Enabled {
		creds, err := loadClientTLSCredentials(cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	conn, err := grpc.NewClient(cfg.Client.Address, opts...)
	if err != nil {
		return nil, fmt.Errorf("create client connection: %w", err)
	}
	return conn, nil
}

func runUnaryClient(cfg *config.Config) error {
	conn, err := dialClient(cfg)
	if err != nil {
		return err
	}
	defer closeConnection(conn)
	client := pb.NewUserServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.Timeout)
	defer cancel()

	user, err := createExampleUser(ctx, client)
	if err != nil {
		return fmt.Errorf("CreateUser failed: %w", err)
	}
	logging.Logger().Info().Str("user_id", user.Id).Msg("user created")
	getUser, err := client.GetUser(ctx, &pb.GetUserRequest{Id: user.Id})
	if err != nil {
		return fmt.Errorf("GetUser failed: %w", err)
	}
	logging.Logger().Info().Str("user_id", getUser.Id).Msg("user retrieved")
	return nil
}

func runServerStreamingClient(cfg *config.Config) error {
	conn, err := dialClient(cfg)
	if err != nil {
		return err
	}
	defer closeConnection(conn)
	client := pb.NewUserServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.Timeout)
	defer cancel()

	user, err := createExampleUser(ctx, client)
	if err != nil {
		return err
	}
	stream, err := client.StreamUserEvents(ctx, &pb.StreamUserEventsRequest{UserId: user.Id, EventTypes: []string{"login", "logout", "purchase"}})
	if err != nil {
		return fmt.Errorf("StreamUserEvents failed: %w", err)
	}
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream recv error: %w", err)
		}
		logging.Logger().Info().Str("event_type", event.EventType).Msg("received event")
	}
}

func runClientStreamingClient(cfg *config.Config) error {
	conn, err := dialClient(cfg)
	if err != nil {
		return err
	}
	defer closeConnection(conn)
	client := pb.NewUserServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.Timeout)
	defer cancel()

	stream, err := client.CollectUserMetrics(ctx)
	if err != nil {
		return fmt.Errorf("CollectUserMetrics failed: %w", err)
	}
	for i := 0; i < 5; i++ {
		if err := stream.Send(&pb.UserMetric{UserId: "client-demo", MetricName: "request_duration", Value: float64(i * 100)}); err != nil {
			return fmt.Errorf("stream send error: %w", err)
		}
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("stream close and recv error: %w", err)
	}
	logging.Logger().Info().Int32("count", resp.Count).Float64("average", resp.Avg).Msg("metrics collected")
	return nil
}

func runBidiStreamingClient(cfg *config.Config) error {
	conn, err := dialClient(cfg)
	if err != nil {
		return err
	}
	defer closeConnection(conn)
	client := pb.NewUserServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.Timeout)
	defer cancel()

	stream, err := client.ChatStream(ctx)
	if err != nil {
		return fmt.Errorf("ChatStream failed: %w", err)
	}
	sendErr := make(chan error, 1)
	go func() {
		defer close(sendErr)
		for i := 0; i < 5; i++ {
			if err := stream.Send(&pb.ChatMessage{UserId: "client-demo", Message: fmt.Sprintf("Hello %d", i)}); err != nil {
				sendErr <- fmt.Errorf("stream send error: %w", err)
				return
			}
		}
		sendErr <- stream.CloseSend()
	}()

	for {
		msg, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			cancel()
			return fmt.Errorf("stream recv error: %w", recvErr)
		}
		logging.Logger().Info().Str("message_id", msg.Id).Msg("received message")
	}
	if err := <-sendErr; err != nil {
		return err
	}
	return nil
}

func createExampleUser(ctx context.Context, client pb.UserServiceClient) (*pb.User, error) {
	user, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Client Demo", Email: fmt.Sprintf("client-%d@example.com", time.Now().UnixNano()), Age: 30})
	if err != nil {
		return nil, fmt.Errorf("CreateUser failed: %w", err)
	}
	return user, nil
}

func loadClientTLSCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if cfg.TLS.CAFile == "" {
		return nil, fmt.Errorf("tls.ca_file is required for the client when TLS is enabled")
	}
	caPEM, err := os.ReadFile(cfg.TLS.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	if ok := rootCAs.AppendCertsFromPEM(caPEM); !ok {
		return nil, fmt.Errorf("client CA contains no valid certificates")
	}
	serverName := cfg.Client.ServerName
	if serverName == "" {
		serverName = strings.Split(cfg.Client.Address, ":")[0]
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCAs, ServerName: serverName}
	if cfg.TLS.MTLS.Enabled {
		if cfg.TLS.ClientCertFile == "" || cfg.TLS.ClientKeyFile == "" {
			return nil, fmt.Errorf("client certificate and key are required when mTLS is enabled")
		}
		clientCert, err := tls.LoadX509KeyPair(cfg.TLS.ClientCertFile, cfg.TLS.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}
	return credentials.NewTLS(tlsConfig), nil
}

func closeConnection(conn *grpc.ClientConn) {
	if conn == nil {
		return
	}
	if err := conn.Close(); err != nil {
		logging.Logger().Error().Err(err).Msg("client connection close failed")
	}
}
