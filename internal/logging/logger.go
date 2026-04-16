package logging

import (
	"flag"
	"log"
	"os"
	"strings"
)

// Level represents the severity of a log message.
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel converts a string level to Level. Defaults to INFO.
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "debug":
		return DEBUG
	case "info":
		return INFO
	case "warn", "warning":
		return WARN
	case "error":
		return ERROR
	default:
		return INFO
	}
}

var (
	// GlobalLogLevel is the current logging threshold.
	GlobalLogLevel Level = INFO

	// LogLevelFlag is the flag name for --log-level (set once per binary).
	LogLevelFlag string
)

// RegisterFlags adds a --log-level flag to the standard flag set.
func RegisterFlags() {
	flag.StringVar(&LogLevelFlag, "log-level", "info", "Minimum log level (debug, info, warn, error)")
}

// InitFromEnvOrFlag reads --log-level flag or LOG_LEVEL env var and sets GlobalLogLevel.
// Call after flag.Parse().
func InitFromEnvOrFlag() {
	env := os.Getenv("LOG_LEVEL")
	if env != "" {
		GlobalLogLevel = ParseLevel(env)
	}
	if LogLevelFlag != "" {
		GlobalLogLevel = ParseLevel(LogLevelFlag)
	}
	log.SetFlags(0) // We prefix every log line ourselves
}

// Debug logs at DEBUG level.
func Debug(format string, args ...interface{}) {
	if GlobalLogLevel > DEBUG {
		return
	}
	log.Printf("[DEBUG] "+format, args...)
}

// Info logs at INFO level.
func Info(format string, args ...interface{}) {
	if GlobalLogLevel > INFO {
		return
	}
	log.Printf("[INFO] "+format, args...)
}

// Warn logs at WARN level.
func Warn(format string, args ...interface{}) {
	if GlobalLogLevel > WARN {
		return
	}
	log.Printf("[WARN] "+format, args...)
}

// Error logs at ERROR level.
func Error(format string, args ...interface{}) {
	if GlobalLogLevel > ERROR {
		return
	}
	log.Printf("[ERROR] "+format, args...)
}
