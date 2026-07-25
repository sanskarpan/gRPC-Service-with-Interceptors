//go:build tracing_e2e

package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
)

func TestTracingE2E_OTLPExport(t *testing.T) {
	mux := http.NewServeMux()
	received := make(chan struct{}, 1)
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		r.Body.Close()
		select {
		case received <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	})
	collectorListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("collector listen: %v", err)
	}
	collectorAddr := collectorListener.Addr().String()
	collector := &http.Server{Addr: collectorAddr, Handler: mux}
	go func() { _ = collector.Serve(collectorListener) }()
	defer func() { _ = collector.Close() }()

	t.Logf("mock OTLP collector on %s", collectorAddr)

	cfg := newBaseTestConfig(t)
	cfg.Tracing.Enabled = true
	cfg.Tracing.Endpoint = "http://" + collectorAddr
	cfg.Tracing.ServiceName = "e2e-test"

	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err = client.CreateUser(ctx, &pb.CreateUserRequest{
		Name: "Trace Test", Email: "trace@test.com", Age: 25,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	server.Stop(t)

	select {
	case <-received:
		t.Log("traces received by mock OTLP collector")
	case <-time.After(5 * time.Second):
		t.Fatal("no traces received within 5s of server shutdown")
	}
}
