package metrics

import (
	"database/sql"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// openIdleDB returns a *sql.DB that is not connected. sql.Open is lazy, so
// db.Stats() is safe and returns a zero-valued snapshot.
func openIdleDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://user:pass@127.0.0.1:1/none")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDBStatsCollectorConcurrentScrapeIsRaceFree(t *testing.T) {
	t.Parallel()
	c := newDBStatsCollector(openIdleDB(t), "raceprobe")
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := reg.Gather(); err != nil {
				t.Errorf("Gather: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestDBStatsCollectorWaitDurationIsCounter(t *testing.T) {
	t.Parallel()
	c := newDBStatsCollector(openIdleDB(t), "typeprobe")
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	const want = "grpc_typeprobe_wait_duration_seconds_total"
	var found *dto.MetricFamily
	for _, mf := range mfs {
		if mf.GetName() == want {
			found = mf
			break
		}
	}
	if found == nil {
		t.Fatalf("metric %q not found in %d families", want, len(mfs))
	}
	if found.GetType() != dto.MetricType_COUNTER {
		t.Fatalf("%s must be a COUNTER (it is monotonic cumulative wait time), got %v", want, found.GetType())
	}
}
