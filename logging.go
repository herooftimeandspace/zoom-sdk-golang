package zoomsdk

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// Logger emits JSON log records.
type Logger struct {
	mu   sync.Mutex
	base *log.Logger
}

// NewLogger creates a logger bound to the given writer.
func NewLogger(writer io.Writer) *Logger {
	if writer == nil {
		writer = io.Discard
	}
	return &Logger{base: log.New(writer, "", 0)}
}

// DefaultLogger returns a disabled-by-default logger.
func DefaultLogger() *Logger {
	return NewLogger(io.Discard)
}

// ConfigureLogging creates a logger that writes JSON records to stderr.
func ConfigureLogging() *Logger {
	return NewLogger(os.Stderr)
}

// Log writes one structured record.
func (l *Logger) Log(level string, message string, fields map[string]any) {
	if l == nil || l.base == nil {
		return
	}
	payload := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"logger":    "zoom_sdk",
		"message":   message,
	}
	for key, value := range fields {
		if value != nil {
			payload[key] = value
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.base.Println(string(encoded))
}
