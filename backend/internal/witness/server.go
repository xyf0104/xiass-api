package witness

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxRequestBodyBytes = 16 * 1024

type Server struct {
	store *Store
	token string
	now   func() time.Time
}

type leaseRequest struct {
	ClusterID  string `json:"cluster_id"`
	NodeID     string `json:"node_id"`
	LeaseID    string `json:"lease_id"`
	Generation int64  `json:"generation,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type leaseResponse struct {
	Lease
	Acquired    bool      `json:"acquired,omitempty"`
	Renewed     bool      `json:"renewed,omitempty"`
	Released    bool      `json:"released,omitempty"`
	RemainingMS int64     `json:"remaining_ms"`
	ServerTime  time.Time `json:"server_time"`
}

func NewServer(store *Store, token string) (*Server, error) {
	token = strings.TrimSpace(token)
	if store == nil {
		return nil, errors.New("witness store is required")
	}
	if len(token) < 32 {
		return nil, errors.New("witness token must contain at least 32 characters")
	}
	return &Server{store: store, token: token, now: time.Now}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /v1/lease/acquire", s.auth(s.acquire))
	mux.HandleFunc("POST /v1/lease/renew", s.auth(s.renew))
	mux.HandleFunc("POST /v1/lease/release", s.auth(s.release))
	mux.HandleFunc("GET /v1/lease/status", s.auth(s.status))
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if len(provided) != len(s.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *Server) acquire(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLeaseRequest(w, r, true, false)
	if !ok {
		return
	}
	now := s.now().UTC()
	lease, acquired, err := s.store.Acquire(r.Context(), req.ClusterID, req.NodeID, req.LeaseID, time.Duration(req.TTLSeconds)*time.Second, now)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store_unavailable"})
		return
	}
	status := http.StatusOK
	if !acquired {
		status = http.StatusConflict
	}
	writeLeaseResponse(w, status, lease, now, func(resp *leaseResponse) { resp.Acquired = acquired })
}

func (s *Server) renew(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLeaseRequest(w, r, true, true)
	if !ok {
		return
	}
	now := s.now().UTC()
	lease, renewed, err := s.store.Renew(r.Context(), req.ClusterID, req.NodeID, req.LeaseID, req.Generation, time.Duration(req.TTLSeconds)*time.Second, now)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store_unavailable"})
		return
	}
	status := http.StatusOK
	if !renewed {
		status = http.StatusConflict
	}
	writeLeaseResponse(w, status, lease, now, func(resp *leaseResponse) { resp.Renewed = renewed })
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLeaseRequest(w, r, false, true)
	if !ok {
		return
	}
	now := s.now().UTC()
	lease, released, err := s.store.Release(r.Context(), req.ClusterID, req.NodeID, req.LeaseID, req.Generation, now)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store_unavailable"})
		return
	}
	status := http.StatusOK
	if !released {
		status = http.StatusConflict
	}
	writeLeaseResponse(w, status, lease, now, func(resp *leaseResponse) { resp.Released = released })
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	clusterID := strings.TrimSpace(r.URL.Query().Get("cluster_id"))
	if !validIdentifier(clusterID, 128) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_cluster_id"})
		return
	}
	now := s.now().UTC()
	lease, err := s.store.Status(r.Context(), clusterID, now)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store_unavailable"})
		return
	}
	writeLeaseResponse(w, http.StatusOK, lease, now, nil)
}

func decodeLeaseRequest(w http.ResponseWriter, r *http.Request, requireTTL, requireGeneration bool) (leaseRequest, bool) {
	var req leaseRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodyBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return leaseRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return leaseRequest{}, false
	}
	req.ClusterID = strings.TrimSpace(req.ClusterID)
	req.NodeID = strings.TrimSpace(req.NodeID)
	req.LeaseID = strings.TrimSpace(req.LeaseID)
	if !validIdentifier(req.ClusterID, 128) || !validIdentifier(req.NodeID, 64) || !validIdentifier(req.LeaseID, 128) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_identity"})
		return leaseRequest{}, false
	}
	if requireTTL && (req.TTLSeconds < 10 || req.TTLSeconds > 60) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_ttl"})
		return leaseRequest{}, false
	}
	if requireGeneration && req.Generation <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_generation"})
		return leaseRequest{}, false
	}
	return req, true
}

func validIdentifier(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func writeLeaseResponse(w http.ResponseWriter, status int, lease Lease, now time.Time, mutate func(*leaseResponse)) {
	remaining := int64(0)
	if lease.Active && lease.ExpiresAt.After(now) {
		remaining = lease.ExpiresAt.Sub(now).Milliseconds()
	}
	response := leaseResponse{Lease: lease, RemainingMS: remaining, ServerTime: now}
	if mutate != nil {
		mutate(&response)
	}
	writeJSON(w, status, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
