package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncParityAssetsCopiesAndChecksVendoredAssets(t *testing.T) {
	tmp := t.TempDir()
	goRoot := filepath.Join(tmp, "zoom-sdk-golang")
	pythonRoot := filepath.Join(tmp, "zoom-sdk-python")

	writeFile := func(path string, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	writeJSON := func(path string, payload any) {
		t.Helper()
		content, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", path, err)
		}
		writeFile(path, string(content))
	}

	writeFile(filepath.Join(goRoot, "go.mod"), "module example.com/zoom-sdk-golang\n")
	writeJSON(filepath.Join(pythonRoot, "src", "zoom_sdk", "endpoints", "users.json"), map[string]any{"paths": map[string]any{}})
	writeJSON(filepath.Join(pythonRoot, "src", "zoom_sdk", "master_accounts", "accounts.json"), map[string]any{"paths": map[string]any{}})
	writeJSON(filepath.Join(pythonRoot, "src", "zoom_sdk", "webhooks", "events.json"), map[string]any{"webhooks": map[string]any{}})
	writeJSON(filepath.Join(pythonRoot, "src", "tests", "golden", "sdk_public_surface.json"), map[string]any{
		"users.list":                map[string]any{},
		"users.get":                 map[string]any{},
		"phone.users.get":           map[string]any{},
		"chat.channels.get_account": map[string]any{},
	})

	if err := syncParityAssets(goRoot, "", false); err != nil {
		t.Fatalf("sync parity assets: %v", err)
	}
	if err := syncParityAssets(goRoot, "", true); err != nil {
		t.Fatalf("expected synced parity assets to pass check mode: %v", err)
	}
	if err := verifyVendoredParity(goRoot); err != nil {
		t.Fatalf("expected vendored parity verification to succeed: %v", err)
	}

	writeFile(filepath.Join(goRoot, "internal", "parity", "golden", "sdk_public_surface.json"), `{"users.list":{}}`)
	if err := verifyVendoredParity(goRoot); err == nil || !strings.Contains(err.Error(), "Missing required vendored SDK operations") {
		t.Fatalf("expected missing operation verification failure, got %v", err)
	}
}

func TestSyncParityAssetsCheckModeReportsDrift(t *testing.T) {
	tmp := t.TempDir()
	goRoot := filepath.Join(tmp, "zoom-sdk-golang")
	pythonRoot := filepath.Join(tmp, "zoom-sdk-python")

	if err := os.MkdirAll(filepath.Join(goRoot, "internal", "parity", "schemas"), 0o755); err != nil {
		t.Fatalf("mkdir go parity root: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pythonRoot, "src", "zoom_sdk", "endpoints"), 0o755); err != nil {
		t.Fatalf("mkdir python endpoints: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pythonRoot, "src", "zoom_sdk", "master_accounts"), 0o755); err != nil {
		t.Fatalf("mkdir python master accounts: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pythonRoot, "src", "zoom_sdk", "webhooks"), 0o755); err != nil {
		t.Fatalf("mkdir python webhooks: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pythonRoot, "src", "tests", "golden"), 0o755); err != nil {
		t.Fatalf("mkdir python golden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module example.com/zoom-sdk-golang\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pythonRoot, "src", "tests", "golden", "sdk_public_surface.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}

	err := syncParityAssets(goRoot, "", true)
	if err == nil || !strings.Contains(err.Error(), "Parity assets are out of sync:") {
		t.Fatalf("expected drift error, got %v", err)
	}
}

func TestParityHelperFunctions(t *testing.T) {
	tmp := t.TempDir()
	left := filepath.Join(tmp, "left")
	right := filepath.Join(tmp, "right")
	if err := os.MkdirAll(filepath.Join(left, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir left: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(right, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir right: %v", err)
	}
	if err := os.WriteFile(filepath.Join(left, "same.txt"), []byte("same"), 0o644); err != nil {
		t.Fatalf("write left same: %v", err)
	}
	if err := os.WriteFile(filepath.Join(right, "same.txt"), []byte("same"), 0o644); err != nil {
		t.Fatalf("write right same: %v", err)
	}
	if err := os.WriteFile(filepath.Join(left, "different.txt"), []byte("left"), 0o644); err != nil {
		t.Fatalf("write left diff: %v", err)
	}
	if err := os.WriteFile(filepath.Join(right, "different.txt"), []byte("right"), 0o644); err != nil {
		t.Fatalf("write right diff: %v", err)
	}
	if err := os.WriteFile(filepath.Join(left, "left-only.txt"), []byte("only"), 0o644); err != nil {
		t.Fatalf("write left only: %v", err)
	}
	if err := os.WriteFile(filepath.Join(right, "right-only.txt"), []byte("only"), 0o644); err != nil {
		t.Fatalf("write right only: %v", err)
	}
	if err := os.WriteFile(filepath.Join(left, "nested", "child.txt"), []byte("nested-left"), 0o644); err != nil {
		t.Fatalf("write nested left: %v", err)
	}
	if err := os.WriteFile(filepath.Join(right, "nested", "child.txt"), []byte("nested-right"), 0o644); err != nil {
		t.Fatalf("write nested right: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(right, "nested")); err != nil {
		t.Fatalf("remove nested dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(right, "nested"), []byte("not-a-dir"), 0o644); err != nil {
		t.Fatalf("write dir/file mismatch: %v", err)
	}

	differences, err := compareTrees(left, right)
	if err != nil {
		t.Fatalf("compare trees: %v", err)
	}
	if len(differences) < 3 {
		t.Fatalf("expected multiple differences, got %#v", differences)
	}

	matches, err := filesEqual(filepath.Join(left, "same.txt"), filepath.Join(right, "same.txt"))
	if err != nil || !matches {
		t.Fatalf("expected matching files, got %v %v", matches, err)
	}
	matches, err = filesEqual(filepath.Join(left, "different.txt"), filepath.Join(right, "different.txt"))
	if err != nil || matches {
		t.Fatalf("expected differing files, got %v %v", matches, err)
	}
	matches, err = filesEqual(filepath.Join(left, "different.txt"), filepath.Join(right, "missing.txt"))
	if err != nil || matches {
		t.Fatalf("expected missing right file to report false, got %v %v", matches, err)
	}

	entries, err := directoryEntries(filepath.Join(tmp, "missing"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected missing directory to behave as empty, got %#v %v", entries, err)
	}
}

func TestCopyHelpersAndErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	sourceDir := filepath.Join(tmp, "source")
	targetDir := filepath.Join(tmp, "target")
	if err := os.MkdirAll(filepath.Join(sourceDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "nested", "file.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	if err := copyTree(sourceDir, targetDir); err != nil {
		t.Fatalf("copy tree: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(targetDir, "nested", "file.txt"))
	if err != nil || string(content) != "payload" {
		t.Fatalf("expected copied payload, got %q %v", string(content), err)
	}

	fileTarget := filepath.Join(tmp, "copied.txt")
	if err := copyFile(filepath.Join(sourceDir, "nested", "file.txt"), fileTarget); err != nil {
		t.Fatalf("copy file: %v", err)
	}
	if _, err := os.Stat(fileTarget); err != nil {
		t.Fatalf("expected copied file to exist: %v", err)
	}
	if err := copyFile(filepath.Join(sourceDir, "missing.txt"), filepath.Join(tmp, "missing.txt")); err == nil {
		t.Fatal("expected copyFile source error")
	}
	if err := copyTree(filepath.Join(tmp, "missing-tree"), filepath.Join(tmp, "other")); err == nil {
		t.Fatal("expected copyTree source error")
	}

	blockingParent := filepath.Join(tmp, "blocking-parent")
	if err := os.WriteFile(blockingParent, []byte("file"), 0o644); err != nil {
		t.Fatalf("write blocking parent: %v", err)
	}
	if err := copyFile(filepath.Join(sourceDir, "nested", "file.txt"), filepath.Join(blockingParent, "child.txt")); err == nil {
		t.Fatal("expected copyFile target parent error")
	}
	if err := copyTree(sourceDir, filepath.Join(blockingParent, "child")); err == nil {
		t.Fatal("expected copyTree target mkdir error")
	}
}

func TestParityPathAndVerificationErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	goRoot := filepath.Join(tmp, "zoom-sdk-golang")
	if err := os.MkdirAll(goRoot, 0o755); err != nil {
		t.Fatalf("mkdir go root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module example.com/zoom-sdk-golang\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if _, err := parityPaths(goRoot, ""); err == nil || !strings.Contains(err.Error(), "zoom-sdk-python source repo not found") {
		t.Fatalf("expected missing python repo error, got %v", err)
	}

	goldenDir := filepath.Join(goRoot, "internal", "parity", "golden")
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goldenDir, "sdk_public_surface.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write invalid golden json: %v", err)
	}
	if err := verifyVendoredParity(goRoot); err == nil {
		t.Fatal("expected invalid golden JSON to fail")
	}
}

func TestParityPathsUsesExplicitPythonRoot(t *testing.T) {
	tmp := t.TempDir()
	goRoot := filepath.Join(tmp, "repo", "zoom-sdk-golang")
	pythonRoot := filepath.Join(tmp, "python-source")
	if err := os.MkdirAll(goRoot, 0o755); err != nil {
		t.Fatalf("mkdir go root: %v", err)
	}
	if err := os.MkdirAll(pythonRoot, 0o755); err != nil {
		t.Fatalf("mkdir python root: %v", err)
	}
	paths, err := parityPaths(goRoot, pythonRoot)
	if err != nil {
		t.Fatalf("parity paths: %v", err)
	}
	if got := paths.schemaPairs[0].source; !strings.HasPrefix(got, pythonRoot) {
		t.Fatalf("expected explicit python root in source path, got %s", got)
	}
}

func TestDirectoryEntriesUnexpectedError(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(filePath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := directoryEntries(filePath); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected non-not-exist directoryEntries error, got %v", err)
	}
}

func TestSyncParityAssetsAndVerificationReadErrors(t *testing.T) {
	tmp := t.TempDir()
	goRoot := filepath.Join(tmp, "zoom-sdk-golang")
	if err := os.MkdirAll(goRoot, 0o755); err != nil {
		t.Fatalf("mkdir go root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module example.com/zoom-sdk-golang\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	if err := syncParityAssets(goRoot, "", false); err == nil {
		t.Fatal("expected sync to fail when zoom-sdk-python is missing")
	}
	if err := verifyVendoredParity(goRoot); err == nil {
		t.Fatal("expected verification to fail when golden file is missing")
	}
}

func TestFilesEqualAndCompareTreesUnexpectedErrors(t *testing.T) {
	tmp := t.TempDir()
	dirPath := filepath.Join(tmp, "dir")
	filePath := filepath.Join(tmp, "file.txt")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := filesEqual(dirPath, filePath); err == nil {
		t.Fatal("expected filesEqual to fail when left side is a directory")
	}
	if _, err := compareTrees(filePath, filepath.Join(tmp, "other")); err == nil {
		t.Fatal("expected compareTrees to fail when source is a file")
	}
}
