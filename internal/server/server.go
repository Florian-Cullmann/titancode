package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/titancode-dev/titancode/internal/project"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	scanner *project.Scanner
	mu      sync.RWMutex
	current project.Snapshot
	hash    string
	clients map[chan []byte]struct{}
}

func New(scanner *project.Scanner) http.Handler {
	server := &Server{
		scanner: scanner,
		clients: make(map[chan []byte]struct{}),
	}
	server.refresh(context.Background())
	go server.watch()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/snapshot", server.snapshot)
	mux.HandleFunc("GET /api/events", server.events)
	content, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", http.FileServer(http.FS(content)))
	return securityHeaders(mux)
}

func (s *Server) snapshot(writer http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(s.current)
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
