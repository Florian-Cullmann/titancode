package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/titancode-dev/titancode/internal/project"
)

func TestSnapshotEndpoint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/snapshot", nil)
	response := httptest.NewRecorder()

	New(project.NewScanner(root)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing")
	}
	var snapshot project.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.SourceFiles != 1 {
		t.Fatalf("source files = %d", snapshot.Summary.SourceFiles)
	}
}

func TestWebAppIsEmbedded(t *testing.T) {
	handler := New(project.NewScanner(t.TempDir()))
	for _, path := range []string{"/", "/changes?file=main.go", "/tests"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if body := response.Body.String(); len(body) < 100 || !contains(body, "TitanCode") {
			t.Fatalf("embedded application was not served for %s", path)
		}
	}
}

func TestTestStateEndpointDetectsGoProject(t *testing.T) {
	root := t.TempDir()
	writeServerTestFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.26\n")
	request := httptest.NewRequest(http.MethodGet, "/api/tests", nil)
	response := httptest.NewRecorder()

	New(project.NewScanner(root)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var state project.TestState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if !state.Available || state.Framework != "Go" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestRunTestsRejectsUnsupportedProject(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/tests/run", nil)
	response := httptest.NewRecorder()

	New(project.NewScanner(t.TempDir())).ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
}

func TestDiffAndStageEndpoints(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init", "-b", "main")
	runTestGit(t, root, "config", "user.email", "test@example.com")
	runTestGit(t, root, "config", "user.name", "Test User")
	writeServerTestFile(t, filepath.Join(root, "app.go"), "package app\n")
	runTestGit(t, root, "add", "app.go")
	runTestGit(t, root, "commit", "-m", "initial")
	writeServerTestFile(t, filepath.Join(root, "app.go"), "package app\n\nfunc Run() {}\n")
	handler := New(project.NewScanner(root))

	diffRequest := httptest.NewRequest(http.MethodGet, "/api/diff?path=app.go&mode=working", nil)
	diffResponse := httptest.NewRecorder()
	handler.ServeHTTP(diffResponse, diffRequest)
	if diffResponse.Code != http.StatusOK || !strings.Contains(diffResponse.Body.String(), "func Run") {
		t.Fatalf("unexpected diff response: %d %s", diffResponse.Code, diffResponse.Body.String())
	}

	stageRequest := httptest.NewRequest(http.MethodPost, "/api/git/stage", bytes.NewBufferString(`{"path":"app.go"}`))
	stageRequest.Header.Set("Content-Type", "application/json")
	stageResponse := httptest.NewRecorder()
	handler.ServeHTTP(stageResponse, stageRequest)
	if stageResponse.Code != http.StatusOK {
		t.Fatalf("unexpected stage response: %d %s", stageResponse.Code, stageResponse.Body.String())
	}
	if output := runTestGit(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(output) != "app.go" {
		t.Fatalf("file was not staged: %q", output)
	}
}

func TestGitActionRejectsCrossOriginRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/git/stage", bytes.NewBufferString(`{"path":"app.go"}`))
	request.Header.Set("Origin", "https://malicious.example")
	response := httptest.NewRecorder()

	New(project.NewScanner(t.TempDir())).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func contains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

func writeServerTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
