package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUsageAndUnknownCommand(t *testing.T) {
	if err := run(nil); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("expected usage error, got %v", err)
	}
	if err := run([]string{"wat"}); err == nil || !strings.Contains(err.Error(), `unknown parity command "wat"`) {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

func TestRunSyncAndVerify(t *testing.T) {
	tmp := t.TempDir()
	goRoot := filepath.Join(tmp, "zoom-sdk-golang")
	pythonRoot := filepath.Join(tmp, "python-source")

	writeJSON := func(path string, payload any) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		content, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := os.MkdirAll(goRoot, 0o755); err != nil {
		t.Fatalf("mkdir go root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module example.com/zoom-sdk-golang\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	writeJSON(filepath.Join(pythonRoot, "src", "zoom_sdk", "endpoints", "users.json"), map[string]any{"paths": map[string]any{}})
	writeJSON(filepath.Join(pythonRoot, "src", "zoom_sdk", "master_accounts", "accounts.json"), map[string]any{"paths": map[string]any{}})
	writeJSON(filepath.Join(pythonRoot, "src", "zoom_sdk", "webhooks", "events.json"), map[string]any{"webhooks": map[string]any{}})
	writeJSON(filepath.Join(pythonRoot, "src", "tests", "golden", "sdk_public_surface.json"), map[string]any{
		"users.list":                map[string]any{},
		"users.get":                 map[string]any{},
		"phone.users.get":           map[string]any{},
		"chat.channels.get_account": map[string]any{},
	})

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(goRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := run([]string{"sync", "--python-root", pythonRoot}); err != nil {
		t.Fatalf("sync command: %v", err)
	}
	if err := run([]string{"sync", "--check", "--python-root", pythonRoot}); err != nil {
		t.Fatalf("sync check command: %v", err)
	}
	if err := run([]string{"verify"}); err != nil {
		t.Fatalf("verify command: %v", err)
	}
}

func TestRunFlagError(t *testing.T) {
	if err := run([]string{"sync", "--nope"}); err == nil {
		t.Fatal("expected sync flag parsing error")
	}

	writer := ioDiscard{}
	if len([]byte("payload")) != mustWrite(t, writer, []byte("payload")) {
		t.Fatal("expected ioDiscard to report bytes written")
	}
}

func mustWrite(t *testing.T, writer ioDiscard, payload []byte) int {
	t.Helper()
	written, err := writer.Write(payload)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return written
}
