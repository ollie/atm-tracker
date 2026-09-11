// Package apitest is a stand-in backend: the tracker endpoints over httptest.
package apitest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	waitTime  = 2 * time.Second
	postDepth = 64
)

type Server struct {
	URL string

	t *testing.T

	mu       sync.Mutex
	flight   *api.FlightInfo
	result   api.PositionsResult
	failWith func(w http.ResponseWriter) bool
	batches  [][]telemetry.Position
	flightID int
	auth     string
	revoked  bool
	posts    chan struct{}
}

func NewServer(t *testing.T) *Server {
	t.Helper()

	s := &Server{
		t:      t,
		result: api.PositionsResult{Status: "enroute"},
		posts:  make(chan struct{}, postDepth),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tracker/flight", s.handleFlight)
	mux.HandleFunc("POST /api/v1/tracker/positions", s.handlePositions)
	mux.HandleFunc("POST /api/v1/oauth/revoke", s.handleRevoke)

	httpServer := httptest.NewServer(mux)
	s.URL = httpServer.URL
	t.Cleanup(httpServer.Close)

	return s
}

func (s *Server) handleFlight(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	item, fail := s.flight, s.failWith
	s.auth = r.Header.Get("Authorization")
	s.mu.Unlock()

	if fail != nil && fail(w) {
		return
	}
	if item == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.writeJSON(w, item)
}

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	var input api.PositionsRequest
	require.NoError(s.t, json.NewDecoder(r.Body).Decode(&input))

	s.mu.Lock()
	result, fail := s.result, s.failWith
	s.batches = append(s.batches, input.Positions)
	s.flightID = input.FlightID
	s.auth = r.Header.Get("Authorization")
	s.mu.Unlock()

	select {
	case s.posts <- struct{}{}:
	default:
	}

	if fail != nil && fail(w) {
		return
	}

	s.writeJSON(w, result)
}

func (s *Server) handleRevoke(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.revoked = true
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) SetFlight(item *api.FlightInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flight = item
}

func (s *Server) SetResult(result api.PositionsResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = result
}

func (s *Server) Fail(with func(w http.ResponseWriter) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failWith = with
}

func (s *Server) WaitForPosts(n int) {
	s.t.Helper()

	for range n {
		select {
		case <-s.posts:
		case <-time.After(waitTime):
			s.t.Fatal("no positions reached the game")
		}
	}
}

func (s *Server) Sent() []telemetry.Position {
	s.mu.Lock()
	defer s.mu.Unlock()

	var all []telemetry.Position
	for _, batch := range s.batches {
		all = append(all, batch...)
	}

	return all
}

func (s *Server) BatchSizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	sizes := make([]int, 0, len(s.batches))
	for _, batch := range s.batches {
		sizes = append(sizes, len(batch))
	}

	return sizes
}

func (s *Server) LastFlightID() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.flightID
}

func (s *Server) LastAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.auth
}

func (s *Server) WasRevoked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.revoked
}

func (s *Server) writeJSON(w http.ResponseWriter, body any) {
	s.t.Helper()

	w.Header().Set("Content-Type", "application/json")
	require.NoError(s.t, json.NewEncoder(w).Encode(body))
}

func ValidationError(w http.ResponseWriter, code string) bool {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = w.Write([]byte(`{"error":"validationError","code":"validation_failed","fields":{"base":{"code":"` + code + `"}}}`))

	return true
}

func Unauthorized(w http.ResponseWriter) bool {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized","code":"unauthorized"}`))

	return true
}

func StaticToken() oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token", TokenType: "Bearer"})
}
