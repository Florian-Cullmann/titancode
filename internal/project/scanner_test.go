package project

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScannerBuildsRepositorySnapshot(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "cmd", "app", "main.go"), "package main\n\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(root, "internal", "orders", "service.go"), "package orders\n\ntype Service struct{}\n")
	writeTestFile(t, filepath.Join(root, "README.md"), "# Project\n")
	writeTestFile(t, filepath.Join(root, "node_modules", "ignored.js"), "const ignored = true;\n")

	snapshot, err := NewScanner(root).Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.Summary.Files != 3 {
		t.Fatalf("files = %d, want 3", snapshot.Summary.Files)
	}
	if snapshot.Summary.SourceFiles != 3 {
		t.Fatalf("source files = %d, want 3", snapshot.Summary.SourceFiles)
	}
	if snapshot.Summary.Modules != 3 {
		t.Fatalf("modules = %d, want 3", snapshot.Summary.Modules)
	}
	if len(snapshot.Languages) != 2 {
		t.Fatalf("languages = %d, want 2", len(snapshot.Languages))
	}
	if snapshot.Project.Name != filepath.Base(root) {
		t.Fatalf("project name = %q", snapshot.Project.Name)
	}
}

func TestScannerReadsGitWorkingTree(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	writeTestFile(t, filepath.Join(root, "app.go"), "package app\n")
	runGit(t, root, "add", "app.go")
	runGit(t, root, "commit", "-m", "initial")
	writeTestFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Run() {}\n")
	writeTestFile(t, filepath.Join(root, "new.go"), "package app\n")

	snapshot, err := NewScanner(root).Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !snapshot.Project.IsGit || snapshot.Project.Branch != "main" {
		t.Fatalf("git project = %#v", snapshot.Project)
	}
	if snapshot.Summary.Changes != 2 {
		t.Fatalf("changes = %d, want 2: %#v", snapshot.Summary.Changes, snapshot.Changes)
	}
	if snapshot.Summary.Insertions != 2 {
		t.Fatalf("insertions = %d, want 2", snapshot.Summary.Insertions)
	}
}

func TestModuleFor(t *testing.T) {
	tests := map[string]string{
		"main.go":                    ".",
		"docs/architecture.md":       "docs",
		"internal/orders/service.go": "internal/orders",
		"src/payments/index.ts":      "src/payments",
	}
	for path, want := range tests {
		_, got := moduleFor(path)
		if got != want {
			t.Errorf("moduleFor(%q) path = %q, want %q", path, got, want)
		}
	}
}

func TestCountLinesIncludesFinalLineWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "one-line.go")
	writeTestFile(t, path, "package example")

	lines, err := countLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines != 1 {
		t.Fatalf("lines = %d, want 1", lines)
	}
}

func TestEmptyCollectionsAreEncodedAsArrays(t *testing.T) {
	snapshot, err := NewScanner(t.TempDir()).Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(payload)
	for _, field := range []string{`"modules":[]`, `"changes":[]`, `"languages":[]`} {
		if !strings.Contains(encoded, field) {
			t.Errorf("snapshot does not contain %s: %s", field, encoded)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
