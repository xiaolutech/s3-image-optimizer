package log

import (
	"log"
	"strings"
)

// Level represents the severity of a log message.
type Level int

const (
	// LevelDebug is the most verbose level.
	LevelDebug Level = iota
	// LevelInfo is the default level.
	LevelInfo
	// LevelWarn is for warnings.
	LevelWarn
	// LevelError is for errors.
	LevelError
)

var levelMap = map[string]Level{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

// Logger is a simple leveled logger.
type Logger struct {
	level Level
}

// New creates a new Logger with the given level string.
// Valid values: "debug", "info", "warn", "error". Defaults to "info".
func New(level string) *Logger {
	lvl := LevelInfo
	if l, ok := levelMap[strings.ToLower(level)]; ok {
		lvl = l
	}
	return &Logger{level: lvl}
}

// Debug logs a message at DEBUG level.
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.level <= LevelDebug {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// Info logs a message at INFO level.
func (l *Logger) Info(format string, args ...interface{}) {
	if l.level <= LevelInfo {
		log.Printf(format, args...)
	}
}

// Warn logs a message at WARN level.
func (l *Logger) Warn(format string, args ...interface{}) {
	if l.level <= LevelWarn {
		log.Printf("[WARN] "+format, args...)
	}
}

// Error logs a message at ERROR level.
func (l *Logger) Error(format string, args ...interface{}) {
	if l.level <= LevelError {
		log.Printf("[ERROR] "+format, args...)
	}
}

// Level returns the current log level.
func (l *Logger) Level() Level {
	return l.level
}

// IsDebug returns true if DEBUG level is enabled.
func (l *Logger) IsDebug() bool {
	return l.level <= LevelDebug
}