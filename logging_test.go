package zoomsdk

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerWritesStructuredJSON(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := NewLogger(buffer)
	logger.Log("INFO", "hello", map[string]any{"event": "request_attempt"})
	output := buffer.String()
	if !strings.Contains(output, `"message":"hello"`) {
		t.Fatalf("unexpected log output: %s", output)
	}
	if !strings.Contains(output, `"event":"request_attempt"`) {
		t.Fatalf("unexpected log output: %s", output)
	}
}
