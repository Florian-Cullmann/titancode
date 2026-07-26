package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	New(project.NewScanner(t.TempDir())).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if body := response.Body.String(); len(body) < 100 || !contains(body, "TitanCode") {
		t.Fatal("embedded application was not served")
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
