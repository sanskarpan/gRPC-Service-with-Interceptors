package metrics

import (
	"database/sql"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "grpc"

// DBStatsCollector exposes sql.DBStats from a database connection pool as
// Prometheus metrics so operators can alert on pool exhaustion, connection
// leaks, and contention. It is stateless: every scrape builds fresh const
// metrics from a db.Stats() snapshot, so concurrent scrapes cannot race and
// counters are reported with their true cumulative values.
type DBStatsCollector struct {
	db *sql.DB

	maxOpen           *prometheus.Desc
	inUse             *prometheus.Desc
	idle              *prometheus.Desc
	waitCount         *prometheus.Desc
	waitDuration      *prometheus.Desc
	maxIdleClosed     *prometheus.Desc
	maxLifetimeClosed *prometheus.Desc
}

// newDBStatsCollector builds a collector without registering it. Used by tests
// that register into an isolated registry.
func newDBStatsCollector(db *sql.DB, subsystem string) *DBStatsCollector {
	sub := "db"
	if subsystem != "" {
		sub = subsystem
	}
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(namespace, sub, name), help, nil, nil)
	}
	return &DBStatsCollector{
		db:                db,
		maxOpen:           desc("max_open_connections", "Maximum number of open connections to the database."),
		inUse:             desc("in_use_connections", "The number of connections currently in use."),
		idle:              desc("idle_connections", "The number of idle connections."),
		waitCount:         desc("wait_count_total", "The total number of connections waited for."),
		waitDuration:      desc("wait_duration_seconds_total", "The total time blocked waiting for a new connection in seconds."),
		maxIdleClosed:     desc("max_idle_closed_total", "The total number of connections closed due to SetMaxIdleConns."),
		maxLifetimeClosed: desc("max_lifetime_closed_total", "The total number of connections closed due to SetConnMaxLifetime."),
	}
}

// NewDBStatsCollector registers a collector that reads sql.DBStats on every
// scrape. The subsystem labels metrics for multi-DB setups.
func NewDBStatsCollector(db *sql.DB, subsystem string) *DBStatsCollector {
	c := newDBStatsCollector(db, subsystem)
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
	ch <- c.maxOpen
	ch <- c.inUse
	ch <- c.idle
	ch <- c.waitCount
	ch <- c.waitDuration
	ch <- c.maxIdleClosed
	ch <- c.maxLifetimeClosed
}

// Collect implements prometheus.Collector. It reads one sql.DBStats snapshot
// and emits fresh const metrics — gauges for the point-in-time pool sizes and
// counters for the cumulative totals.
func (c *DBStatsCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.db.Stats()
	ch <- prometheus.MustNewConstMetric(c.maxOpen, prometheus.GaugeValue, float64(stats.MaxOpenConnections))
	ch <- prometheus.MustNewConstMetric(c.inUse, prometheus.GaugeValue, float64(stats.InUse))
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(stats.Idle))
	ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(stats.WaitCount))
	ch <- prometheus.MustNewConstMetric(c.waitDuration, prometheus.CounterValue, stats.WaitDuration.Seconds())
	ch <- prometheus.MustNewConstMetric(c.maxIdleClosed, prometheus.CounterValue, float64(stats.MaxIdleClosed))
	ch <- prometheus.MustNewConstMetric(c.maxLifetimeClosed, prometheus.CounterValue, float64(stats.MaxLifetimeClosed))
}
