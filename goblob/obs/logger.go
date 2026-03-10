package obs

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// LogLevel is the runtime log level selector.
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

var (
	logMu     sync.Mutex
	levelVar            = &slog.LevelVar{}
	logOutput io.Writer = os.Stderr
	logFormat           = "json"
)

func init() {
	levelVar.Set(slog.LevelInfo)
	rebuildDefaultLogger()
}

// New returns a logger with subsystem pre-attached.
func New(subsystem string) *slog.Logger {
	if strings.TrimSpace(subsystem) == "" {
		return slog.Default()
	}
	return slog.Default().With("subsystem", subsystem)
}

// SetLevel updates runtime logging level atomically.
func SetLevel(level LogLevel) {
	switch strings.ToLower(string(level)) {
	case string(LevelDebug):
		levelVar.Set(slog.LevelDebug)
	case string(LevelWarn):
		levelVar.Set(slog.LevelWarn)
	case string(LevelError):
		levelVar.Set(slog.LevelError)
	default:
		levelVar.Set(slog.LevelInfo)
	}
}

// SetOutput reconfigures the default logger output.
func SetOutput(w io.Writer) {
	if w == nil {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	logOutput = w
	rebuildDefaultLogger()
}

// SetFormat configures output format: "json" or "text".
func SetFormat(format string) {
	f := strings.ToLower(strings.TrimSpace(format))
	if f != "json" && f != "text" {
		f = "json"
	}
	logMu.Lock()
	defer logMu.Unlock()
	logFormat = f
	rebuildDefaultLogger()
}

func rebuildDefaultLogger() {
	opts := &slog.HandlerOptions{Level: levelVar}
	var h slog.Handler
	if logFormat == "text" {
		h = slog.NewTextHandler(logOutput, opts)
	} else {
		h = slog.NewJSONHandler(logOutput, opts)
	}
	slog.SetDefault(slog.New(h))
}
