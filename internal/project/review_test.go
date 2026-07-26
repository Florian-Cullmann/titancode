package project

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffReadsWorkingAndStagedChanges(t *testing.T) {
	root := initializedRepository(t)
	path := filepath.Join(root, "app.go")
	writeTestFile(t, path, "package app\n\nfunc Run() string { return \"changed\" }\n")
	scanner := NewScanner(root)

	working, err := scanner.Diff(context.Background(), "app.go", "working")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(working.Content, `+func Run() string { return "changed" }`) {
		t.Fatalf("working diff does not contain change:\n%s", working.Content)
	}

	if err := scanner.Stage(context.Background(), "app.go"); err != nil {
		t.Fatal(err)
	}
	staged, err := scanner.Diff(context.Background(), "app.go", "staged")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(staged.Content, `+func Run() string { return "changed" }`) {
		t.Fatalf("staged diff does not contain change:\n%s", staged.Content)
	}

	if err := scanner.Unstage(context.Background(), "app.go"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Changes) != 1 || snapshot.Changes[0].Staged || !snapshot.Changes[0].Unstaged {
		t.Fatalf("unexpected change after unstage: %#v", snapshot.Changes)
	}
}

func TestDiffReadsUntrackedFile(t *testing.T) {
	root := initializedRepository(t)
	writeTestFile(t, filepath.Join(root, "new.txt"), "first\nsecond\n")

	diff, err := NewScanner(root).Diff(context.Background(), "new.txt", "working")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Content, "+first") || !strings.Contains(diff.Content, "+second") {
		t.Fatalf("untracked diff is incomplete:\n%s", diff.Content)
	}
}

func TestReviewRejectsPathsOutsideChangeSet(t *testing.T) {
	root := initializedRepository(t)
	scanner := NewScanner(root)

	for _, path := range []string{"../secret", "/etc/passwd", "app.go"} {
		if _, err := scanner.Diff(context.Background(), path, "working"); err == nil {
			t.Errorf("Diff(%q) should fail", path)
		}
	}
}

func initializedRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	writeTestFile(t, filepath.Join(root, "app.go"), "package app\n")
	runGit(t, root, "add", "app.go")
	runGit(t, root, "commit", "-m", "initial")
	return root
}
