package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

var requiredVendoredOperations = []string{
	"users.list",
	"users.get",
	"phone.users.get",
	"chat.channels.get_account",
}

func syncParityAssets(repoRoot string, checkOnly bool) error {
	paths, err := parityPaths(repoRoot)
	if err != nil {
		return err
	}

	if checkOnly {
		differences, err := parityDifferences(paths)
		if err != nil {
			return err
		}
		if len(differences) == 0 {
			return nil
		}
		return fmt.Errorf("%s", renderParityDifferences(differences))
	}

	if err := os.MkdirAll(paths.schemasRoot, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.goldenRoot, 0o755); err != nil {
		return err
	}
	for _, pair := range paths.schemaPairs {
		if err := copyTree(pair.source, pair.target); err != nil {
			return err
		}
	}
	for _, pair := range paths.goldenPairs {
		if err := copyFile(pair.source, pair.target); err != nil {
			return err
		}
	}
	return nil
}

func verifyVendoredParity(repoRoot string) error {
	root := repoRoot
	if root == "" {
		root = discoverProjectRoot(".")
	}
	goldenPath := filepath.Join(root, "internal", "parity", "golden", "sdk_public_surface.json")
	payload, err := os.ReadFile(goldenPath)
	if err != nil {
		return err
	}
	var operations map[string]map[string]any
	if err := json.Unmarshal(payload, &operations); err != nil {
		return err
	}
	missing := []string{}
	for _, operation := range requiredVendoredOperations {
		if _, ok := operations[operation]; !ok {
			missing = append(missing, operation)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	message := "Missing required vendored SDK operations:"
	for _, operation := range missing {
		message += "\n" + operation
	}
	return fmt.Errorf("%s", message)
}

type parityPair struct {
	source string
	target string
}

type paritySyncPaths struct {
	schemasRoot string
	goldenRoot  string
	schemaPairs []parityPair
	goldenPairs []parityPair
}

func parityPaths(repoRoot string) (*paritySyncPaths, error) {
	root := repoRoot
	if root == "" {
		root = discoverProjectRoot(".")
	}
	pythonRoot := os.Getenv("ZOOM_SDK_PYTHON_ROOT")
	if pythonRoot == "" {
		pythonRoot = filepath.Join(filepath.Dir(root), "zoom-sdk-python")
	}
	if _, err := os.Stat(pythonRoot); err != nil {
		return nil, fmt.Errorf("zoom-sdk-python source repo not found at %s", pythonRoot)
	}

	parityRoot := filepath.Join(root, "internal", "parity")
	schemasRoot := filepath.Join(parityRoot, "schemas")
	goldenRoot := filepath.Join(parityRoot, "golden")
	return &paritySyncPaths{
		schemasRoot: schemasRoot,
		goldenRoot:  goldenRoot,
		schemaPairs: []parityPair{
			{
				source: filepath.Join(pythonRoot, "src", "zoom_sdk", "endpoints"),
				target: filepath.Join(schemasRoot, "endpoints"),
			},
			{
				source: filepath.Join(pythonRoot, "src", "zoom_sdk", "master_accounts"),
				target: filepath.Join(schemasRoot, "master_accounts"),
			},
			{
				source: filepath.Join(pythonRoot, "src", "zoom_sdk", "webhooks"),
				target: filepath.Join(schemasRoot, "webhooks"),
			},
		},
		goldenPairs: []parityPair{
			{
				source: filepath.Join(pythonRoot, "src", "tests", "golden", "sdk_public_surface.json"),
				target: filepath.Join(goldenRoot, "sdk_public_surface.json"),
			},
		},
	}, nil
}

func parityDifferences(paths *paritySyncPaths) ([]string, error) {
	differences := []string{}
	for _, pair := range paths.schemaPairs {
		pairDifferences, err := compareTrees(pair.source, pair.target)
		if err != nil {
			return nil, err
		}
		differences = append(differences, pairDifferences...)
	}
	for _, pair := range paths.goldenPairs {
		matches, err := filesEqual(pair.source, pair.target)
		if err != nil {
			return nil, err
		}
		if !matches {
			differences = append(differences, pair.source)
		}
	}
	sort.Strings(differences)
	return differences, nil
}

func renderParityDifferences(differences []string) string {
	message := "Parity assets are out of sync:"
	for _, difference := range differences {
		message += "\n" + difference
	}
	return message
}

func copyTree(source string, target string) error {
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	return os.CopyFS(target, os.DirFS(source))
}

func copyFile(source string, target string) error {
	input, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, input, 0o644)
}

func compareTrees(source string, target string) ([]string, error) {
	sourceEntries, err := directoryEntries(source)
	if err != nil {
		return nil, err
	}
	targetEntries, err := directoryEntries(target)
	if err != nil {
		return nil, err
	}

	differences := []string{}
	for name, sourceEntry := range sourceEntries {
		targetEntry, ok := targetEntries[name]
		if !ok {
			differences = append(differences, filepath.Join(source, name))
			continue
		}
		sourcePath := filepath.Join(source, name)
		targetPath := filepath.Join(target, name)
		if sourceEntry.IsDir() != targetEntry.IsDir() {
			differences = append(differences, sourcePath)
			continue
		}
		if sourceEntry.IsDir() {
			childDifferences, err := compareTrees(sourcePath, targetPath)
			if err != nil {
				return nil, err
			}
			differences = append(differences, childDifferences...)
			continue
		}
		matches, err := filesEqual(sourcePath, targetPath)
		if err != nil {
			return nil, err
		}
		if !matches {
			differences = append(differences, sourcePath)
		}
	}
	for name := range targetEntries {
		if _, ok := sourceEntries[name]; !ok {
			differences = append(differences, filepath.Join(target, name))
		}
	}
	sort.Strings(differences)
	return differences, nil
}

func directoryEntries(path string) (map[string]fs.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]fs.DirEntry{}, nil
		}
		return nil, err
	}
	result := map[string]fs.DirEntry{}
	for _, entry := range entries {
		result[entry.Name()] = entry
	}
	return result, nil
}

func filesEqual(left string, right string) (bool, error) {
	leftBytes, err := os.ReadFile(left)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	rightBytes, err := os.ReadFile(right)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(leftBytes, rightBytes), nil
}

func discoverProjectRoot(start string) string {
	current, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		current = parent
	}
}
