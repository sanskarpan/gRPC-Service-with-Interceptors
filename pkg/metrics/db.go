package metrics

import (
	"database/sql"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const namespace = "grpc"

// DBStatsCollector exposes sql.DBStats from a database connection pool as
// Prometheus metrics so operators can alert on pool exhaustion, connection
// leaks, and contention.
type DBStatsCollector struct {
	db *sql.DB

	maxOpen        prometheus.Gauge
	inUse          prometheus.Gauge
	idle           prometheus.Gauge
	waitCount      prometheus.Counter
	waitDuration   prometheus.Gauge
	maxIdleClosed  prometheus.Counter
	maxLifetimeClosed prometheus.Counter
}

// NewDBStatsCollector registers a collector that reads sql.DBStats on every
// scrape. The prefix is used to label metrics for multi-DB setups.
func NewDBStatsCollector(db *sql.DB, subsystem string) *DBStatsCollector {
	sub := "db"
	if subsystem != "" {
		sub = subsystem
	}
	c := &DBStatsCollector{
		db: db,
		maxOpen: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prometheus.BuildFQName(namespace, sub, "max_open_connections"),
			Help: "Maximum number of open connections to the database.",
		}),
		inUse: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prometheus.BuildFQName(namespace, sub, "in_use_connections"),
			Help: "The number of connections currently in use.",
		}),
		idle: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prometheus.BuildFQName(namespace, sub, "idle_connections"),
			Help: "The number of idle connections.",
		}),
		waitCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prometheus.BuildFQName(namespace, sub, "wait_count_total"),
			Help: "The total number of connections waited for.",
		}),
		waitDuration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prometheus.BuildFQName(namespace, sub, "wait_duration_seconds_total"),
			Help: "The total time blocked waiting for a new connection in seconds.",
		}),
		maxIdleClosed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prometheus.BuildFQName(namespace, sub, "max_idle_closed_total"),
			Help: "The total number of connections closed due to SetMaxIdleConns.",
		}),
		maxLifetimeClosed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prometheus.BuildFQName(namespace, sub, "max_lifetime_closed_total"),
			Help: "The total number of connections closed due to SetConnMaxLifetime.",
		}),
	}
	if err := prometheus.Register(c); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			return already.ExistingCollector.(*DBStatsCollector)
		}
	}
	return c
}

// Describe implements prometheus.Collector.
func (c *DBStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	c.maxOpen.Describe(ch)
	c.inUse.Describe(ch)
	c.idle.Describe(ch)
	c.waitCount.Describe(ch)
	c.waitDuration.Describe(ch)
	c.maxIdleClosed.Describe(ch)
	c.maxLifetimeClosed.Describe(ch)
}

// Collect implements prometheus.Collector. It reads the latest sql.DBStats
// snapshot and updates the gauges before collecting them.
func (c *DBStatsCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.db.Stats()
	c.maxOpen.Set(float64(stats.MaxOpenConnections))
	c.inUse.Set(float64(stats.InUse))
	c.idle.Set(float64(stats.Idle))
	c.waitCount.Add(float64(stats.WaitCount - int64(c.waitCountValue())))
	c.waitDuration.Set(stats.WaitDuration.Seconds())
	c.maxIdleClosed.Add(float64(stats.MaxIdleClosed - int64(c.maxIdleClosedValue())))
	c.maxLifetimeClosed.Add(float64(stats.MaxLifetimeClosed - int64(c.maxLifetimeClosedValue())))

	c.maxOpen.Collect(ch)
	c.inUse.Collect(ch)
	c.idle.Collect(ch)
	c.waitCount.Collect(ch)
	c.waitDuration.Collect(ch)
	c.maxIdleClosed.Collect(ch)
	c.maxLifetimeClosed.Collect(ch)
}

// waitCountValue returns the current counter value for the waitCount metric.
func (c *DBStatsCollector) waitCountValue() float64 {
	var m dto.Metric
	if err := c.waitCount.Write(&m); err != nil {
		return 0
	}
	if m.Counter == nil {
		return 0
	}
	return m.Counter.GetValue()
}

// maxIdleClosedValue returns the current counter value for the maxIdleClosed metric.
func (c *DBStatsCollector) maxIdleClosedValue() float64 {
	var m dto.Metric
	if err := c.maxIdleClosed.Write(&m); err != nil {
		return 0
	}
	if m.Counter == nil {
		return 0
	}
	return m.Counter.GetValue()
}

// maxLifetimeClosedValue returns the current counter value for the maxLifetimeClosed metric.
func (c *DBStatsCollector) maxLifetimeClosedValue() float64 {
	var m dto.Metric
	if err := c.maxLifetimeClosed.Write(&m); err != nil {
		return 0
	}
	if m.Counter == nil {
		return 0
	}
	return m.Counter.GetValue()
}
