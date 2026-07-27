package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/titancode-dev/titancode/internal/project"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	scanner *project.Scanner
	tester  *project.TestRunner
	mu      sync.RWMutex
	current project.Snapshot
	hash    string
	clients map[chan []byte]struct{}
}

func New(scanner *project.Scanner) http.Handler {
	server := &Server{
		scanner: scanner,
		tester:  project.NewTestRunner(scanner.Root()),
		clients: make(map[chan []byte]struct{}),
	}
	server.refresh(context.Background())
	go server.watch()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/snapshot", server.snapshot)
	mux.HandleFunc("GET /api/events", server.events)
	mux.HandleFunc("GET /api/diff", server.diff)
	mux.HandleFunc("POST /api/git/stage", server.stage)
	mux.HandleFunc("POST /api/git/unstage", server.unstage)
	mux.HandleFunc("GET /api/tests", server.testState)
	mux.HandleFunc("POST /api/tests/run", server.runTests)
	mux.HandleFunc("POST /api/tests/cancel", server.cancelTests)
	content, _ := fs.Sub(webFiles, "web")
	mux.HandleFunc("GET /changes", serveIndex(content))
	mux.HandleFunc("GET /tests", serveIndex(content))
	mux.Handle("/", http.FileServer(http.FS(content)))
	return securityHeaders(mux)
}

func (s *Server) testState(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, s.tester.State())
}

func (s *Server) runTests(writer http.ResponseWriter, request *http.Request) {
	s.testAction(writer, request, s.tester.Start)
}

func (s *Server) cancelTests(writer http.ResponseWriter, request *http.Request) {
	s.testAction(writer, request, s.tester.Cancel)
}

func (s *Server) testAction(writer http.ResponseWriter, request *http.Request, action func() error) {
	if !sameOrigin(request) {
		writeError(writer, http.StatusForbidden, errors.New("cross-origin request rejected"))
		return
	}
	if err := action(); err != nil {
		writeError(writer, http.StatusConflict, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, s.tester.State())
}

func serveIndex(content fs.FS) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		index, err := fs.ReadFile(content, "index.html")
		if err != nil {
			http.Error(writer, "application unavailable", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write(index)
	}
}

func (s *Server) diff(writer http.ResponseWriter, request *http.Request) {
	diff, err := s.scanner.Diff(request.Context(), request.URL.Query().Get("path"), request.URL.Query().Get("mode"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusOK, diff)
}

func (s *Server) stage(writer http.ResponseWriter, request *http.Request) {
	s.gitAction(writer, request, s.scanner.Stage)
}

func (s *Server) unstage(writer http.ResponseWriter, request *http.Request) {
	s.gitAction(writer, request, s.scanner.Unstage)
}

func (s *Server) gitAction(
	writer http.ResponseWriter,
	request *http.Request,
	action func(context.Context, string) error,
) {
	if !sameOrigin(request) {
		writeError(writer, http.StatusForbidden, errors.New("cross-origin request rejected"))
		return
	}
	var payload struct {
		Path string `json:"path"`
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 4096)
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	if err := action(request.Context(), payload.Path); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	s.refresh(request.Context())
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) snapshot(writer http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(writer, http.StatusOK, s.current)
}

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == request.Host &&
		(parsed.Scheme == "http" || parsed.Scheme == "https")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}

func (s *Server) events(writer http.ResponseWriter, request *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")

	client := make(chan []byte, 1)
	s.mu.Lock()
	s.clients[client] = struct{}{}
	initial, _ := json.Marshal(s.current)
	s.mu.Unlock()
	client <- initial

	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
	}()

	for {
		select {
		case payload := <-client:
			_, _ = writer.Write([]byte("event: snapshot\ndata: "))
			_, _ = writer.Write(payload)
			_, _ = writer.Write([]byte("\n\n"))
			flusher.Flush()
		case <-request.Context().Done():
			return
		}
	}
}

func (s *Server) watch() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.refresh(context.Background())
	}
}

func (s *Server) refresh(ctx context.Context) {
	snapshot, err := s.scanner.Scan(ctx)
	if err != nil {
		log.Printf("scan failed: %v", err)
		return
	}
	payload, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	if hash == s.hash {
		return
	}
	s.current = snapshot
	s.hash = hash
	for client := range s.clients {
		select {
		case client <- payload:
		default:
		}
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}
