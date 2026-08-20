// Package testoscar provides a deterministic HTTP seam for OSCAR adapter tests.
package testoscar

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// Response is one ordered fake-server response.
type Response struct {
	Status  int
	Header  http.Header
	Body    string
	Release <-chan struct{}
}

// Request is sanitized recorded request evidence.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   string
}

// Server is an ordered scripted httptest server.
type Server struct {
	t        testing.TB
	server   *httptest.Server
	mu       sync.Mutex
	queue    []Response
	requests []Request
}

// New starts a server and registers automatic shutdown with the test.
func New(t testing.TB) *Server {
	t.Helper()
	result := &Server{t: t}
	result.server = httptest.NewServer(http.HandlerFunc(result.handle))
	t.Cleanup(result.server.Close)
	return result
}

// URL returns the listener URL.
func (s *Server) URL() string { return s.server.URL }

// Enqueue appends an expected response.
func (s *Server) Enqueue(response Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if response.Status == 0 {
		response.Status = http.StatusOK
	}
	s.queue = append(s.queue, response)
}

// Requests returns a copy of sanitized observations.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Request, len(s.requests))
	copy(result, s.requests)
	return result
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "could not record request", http.StatusBadRequest)
		return
	}
	recorded := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: string(body)}
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key"} {
		if recorded.Header.Get(name) != "" {
			recorded.Header.Set(name, "[REDACTED]")
		}
	}

	s.mu.Lock()
	s.requests = append(s.requests, recorded)
	if len(s.queue) == 0 {
		s.mu.Unlock()
		s.t.Errorf("unexpected OSCAR test request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
		return
	}
	response := s.queue[0]
	s.queue = s.queue[1:]
	s.mu.Unlock()

	if response.Release != nil {
		select {
		case <-response.Release:
		case <-r.Context().Done():
			return
		}
	}
	for name, values := range response.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.Status)
	_, _ = io.WriteString(w, response.Body)
}
