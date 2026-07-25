package logging

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// setTempLogger swaps the process-wide logger for the lifetime of fn so the
// test can capture output without the global atomic pointer races.
func setTempLogger(w *os.File, fn func()) {
	l := zerolog.New(w).With().Timestamp().Logger()
	old := Logger()
	SetLogger(&l)
	defer func() { SetLogger(old) }()
	fn()
}

func TestLoggerDefaultBeforeInit(t *testing.T) {
	r, w, _ := os.Pipe()
	setTempLogger(w, func() {
		Logger().Info().Msg("hello default")
	})
	w.Close()
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "hello default") {
		t.Errorf("expected output to contain message, got: %s", string(out))
	}
}

func TestLoggerInitJSON(t *testing.T) {
	Init("info", "json")
	r, w, _ := os.Pipe()
	setTempLogger(w, func() {
		Logger().Info().Msg("json test message")
	})
	w.Close()
	out, _ := io.ReadAll(r)
	var js map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &js); err != nil {
		t.Fatalf("expected valid JSON: %v\nraw output: %s", err, string(out))
	}
	if js["message"] != "json test message" {
		t.Errorf("expected message='json test message', got message=%v", js["message"])
	}
}

func TestLoggerInitConsole(t *testing.T) {
	Init("debug", "console")
	r, w, _ := os.Pipe()
	setTempLogger(w, func() {
		Logger().Debug().Msg("console message")
	})
	w.Close()
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "console message") {
		t.Errorf("expected output to contain message, got: %s", string(out))
	}
}

func TestLoggerInitInvalidLevel(t *testing.T) {
	Init("not-a-level", "json")

	r, w, _ := os.Pipe()
	setTempLogger(w, func() {
		Logger().Debug().Msg("debug-hidden")
	})
	w.Close()
	out, _ := io.ReadAll(r)
	if strings.Contains(string(out), "debug-hidden") {
		t.Error("debug message should not appear when level defaults to info")
	}

	r2, w2, _ := os.Pipe()
	setTempLogger(w2, func() {
		Logger().Info().Msg("info-visible")
	})
	w2.Close()
	out2, _ := io.ReadAll(r2)
	if !strings.Contains(string(out2), "info-visible") {
		t.Error("info message should appear")
	}
}

func TestLoggerWith(t *testing.T) {
	ctx := With()
	l := ctx.Logger()
	if !l.Info().Enabled() {
		t.Error("logger returned by With() should be enabled at info level")
	}
}

func TestLoggerInitLevelFiltering(t *testing.T) {
	Init("error", "json")

	r, w, _ := os.Pipe()
	setTempLogger(w, func() {
		Logger().Info().Msg("info-hidden")
		Logger().Error().Msg("error-visible")
	})
	w.Close()
	out, _ := io.ReadAll(r)
	if strings.Contains(string(out), "info-hidden") {
		t.Error("info message should not appear at error level")
	}
	if !strings.Contains(string(out), "error-visible") {
		t.Error("error message should appear at error level")
	}
}
