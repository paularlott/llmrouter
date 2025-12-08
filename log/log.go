package log

import (
	"os"

	"github.com/paularlott/logger"
	logslog "github.com/paularlott/logger/slog"
)

var defaultLogger logger.Logger

func init() {
	// Initialize with default configuration
	defaultLogger = logslog.New(logslog.Config{
		Level:  "info",
		Format: "console",
		Writer: os.Stdout,
	})
}

// Configure sets up the logger
func Configure(level, format string) {
	defaultLogger = logslog.New(logslog.Config{
		Level:  level,
		Format: format,
		Writer: os.Stdout,
	})
}

// Package-level functions for convenience
func Info(msg string, keysAndValues ...any) {
	defaultLogger.Info(msg, keysAndValues...)
}

func Debug(msg string, keysAndValues ...any) {
	defaultLogger.Debug(msg, keysAndValues...)
}

func Warn(msg string, keysAndValues ...any) {
	defaultLogger.Warn(msg, keysAndValues...)
}

func Error(msg string, keysAndValues ...any) {
	defaultLogger.Error(msg, keysAndValues...)
}

func With(key string, value any) logger.Logger {
	return defaultLogger.With(key, value)
}

func WithError(err error) logger.Logger {
	return defaultLogger.WithError(err)
}

func GetLogger() logger.Logger {
	return defaultLogger
}