package observability

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger creates a zap.Logger.
// If json=true, uses JSON encoding (production). Otherwise uses console (dev).
func NewLogger(level zapcore.Level, json bool) *zap.Logger {
	var config zap.Config
	if json {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
	}

	config.Level = zap.NewAtomicLevelAt(level)
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.EncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	logger, err := config.Build()
	if err != nil {
		// Fallback to default logger
		return zap.NewNop()
	}

	return logger
}

// ReplaceGlobal sets the package-level logger used by zap.L() and zap.S().
func ReplaceGlobal(l *zap.Logger) {
	zap.ReplaceGlobals(l)
}

// NewNopLogger returns a no-op logger for testing.
func NewNopLogger() *zap.Logger {
	return zap.NewNop()
}

// ParseLevel parses a log level string (debug, info, warn, error, fatal).
func ParseLevel(s string) zapcore.Level {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return zapcore.InfoLevel
	}
	return level
}
