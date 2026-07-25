// Package metrics defines the Prometheus instruments used by the gRPC server.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	dto "github.com/prometheus/client_model/go"
)

var (
	// TotalRequests counts inbound gRPC requests by method.
	TotalRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_server_total_requests",
			Help: "Total number of gRPC requests",
		},
		[]string{"method"},
	)

	// TotalErrors counts inbound gRPC failures by method and status code.
	TotalErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_server_total_errors",
			Help: "Total number of gRPC errors",
		},
		[]string{"method", "code"},
	)

	// RequestDuration measures inbound gRPC latency in seconds.
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_server_request_duration_seconds",
			Help:    "gRPC request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	// ActiveStreams reports the number of active inbound streams.
	ActiveStreams = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "grpc_server_active_streams",
			Help: "Number of active gRPC streams",
		},
	)
)

// IncTotalRequests records one inbound gRPC request for the method.
func IncTotalRequests(method string) {
	TotalRequests.WithLabelValues(method).Inc()
}

// IncTotalErrors records an inbound gRPC request that ended with a status code.
func IncTotalErrors(method, code string) {
	TotalErrors.WithLabelValues(method, code).Inc()
}

// ObserveRequestDuration records the elapsed inbound request duration in seconds.
func ObserveRequestDuration(method string, duration float64) {
	RequestDuration.WithLabelValues(method).Observe(duration)
}

// IncActiveStreams increments the number of currently active server streams.
func IncActiveStreams() {
	ActiveStreams.Inc()
}

// DecActiveStreams decrements the number of currently active server streams.
// It clamps at zero so a transient gauge drift or test-induced over-decrement
// never produces a negative gauge that would mislead operators.
func DecActiveStreams() {
	if ActiveStreams == nil {
		return
	}
	if getGaugeValue(ActiveStreams) > 0 {
		ActiveStreams.Dec()
	}
}

// getGaugeValue returns the current value of a prometheus.Gauge using the
// internal dto representation. It returns 0 when the gauge is unset.
func getGaugeValue(g prometheus.Gauge) float64 {
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		return 0
	}
	if m.Gauge == nil {
		return 0
	}
	return m.GetGauge().GetValue()
}
