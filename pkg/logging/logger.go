// Package logging provides the process-wide structured logger used by service
// boundaries and lifecycle code.
package logging

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// logger stores the process-wide structured logger behind an atomic pointer so
// that concurrent reads from With() never block. Init serialises the zerolog
// global setup (TimeFieldFormat, global level) behind initMu.
var (
	logger atomic.Pointer[zerolog.Logger]
	initMu sync.Mutex
)

func init() {
	l := zerolog.New(os.Stdout).With().Timestamp().Logger()
	logger.Store(&l)
}

// Logger returns the currently configured process-wide logger.
func Logger() *zerolog.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	nop := zerolog.Nop()
	return &nop
}

// Init configures the process-wide logger after configuration validation.
// Init synchronises writes to the zerolog package-level globals and the local
// Logger pointer so that parallel test servers never data-race on either.
func Init(level, format string) {
	initMu.Lock()
	defer initMu.Unlock()
	zerolog.TimeFieldFormat = time.RFC3339Nano

	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	var l zerolog.Logger
	if format == "json" {
		l = zerolog.New(os.Stdout).With().Timestamp().Logger()
	} else {
		l = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).With().Timestamp().Logger()
	}
	logger.Store(&l)
}

// SetLevel changes the global log level at runtime (e.g. on SIGHUP during an
// incident) without re-initializing the logger. zerolog's global level is
// atomic, so this is safe to call concurrently with logging.
func SetLevel(level string) error {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		return err
	}
	zerolog.SetGlobalLevel(lvl)
	return nil
}

// With starts a structured logging context from the configured logger.
func With() zerolog.Context {
	return Logger().With()
}

// SetLogger replaces the process-wide logger. It is exported for tests that
// need to capture log output; production code must use Init instead.
func SetLogger(l *zerolog.Logger) {
	logger.Store(l)
}
