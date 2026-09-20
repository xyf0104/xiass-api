package control

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/xyf0104/xiass-proxy-agent/internal/proxyroute"
)

// Inline source JSON escaping and opaque snapshot encoding can expand the
// upstream 16 x 2 MiB source limit. Keep a bounded authenticated-local envelope.
const maxJSONBody = proxyroute.MaxSubscriptionSources * proxyroute.MaxSubscriptionBytes * 4

type Server struct {
	token   string
	manager *Manager
}

type parseRequest struct {
	Input string `json:"input"`
}

type createRequest struct {
	Input string `json:"input"`
	Index int    `json:"index"`
}

type subscriptionPreviewRequest struct {
	Sources []proxyroute.Source `json:"sources"`
}

type subscriptionSyncRequest struct {
	Snapshot        string            `json:"snapshot,omitempty"`
	Selected        []string          `json:"selected_node_ids"`
	ListenAddresses map[string]string `json:"listen_addresses,omitempty"`
	Prune           *bool             `json:"prune,omitempty"`
}

func NewServer(token string, manager *Manager) (*Server, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("control API token is required")
	}
	if manager == nil {
		manager = NewManager()
	}
	return &Server{token: token, manager: manager}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/v1/preview/parse", s.preview)
	mux.HandleFunc("/v1/subscriptions/preview", s.subscriptionPreview)
	mux.HandleFunc("/v1/subscriptions/sync", s.subscriptionSync)
	mux.HandleFunc("/v1/routes", s.routes)
	mux.HandleFunc("/v1/routes/", s.route)
	return authMiddleware(s.token, mux)
}

func (s *Server) subscriptionPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request subscriptionPreviewRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	result, err := s.manager.Preview(r.Context(), request.Sources)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) subscriptionSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request subscriptionSyncRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	prune := true
	if request.Prune != nil {
		prune = *request.Prune
	}
	result, err := s.manager.Sync(r.Context(), request.Snapshot, request.Selected, request.ListenAddresses, prune)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("Authorization")
		if strings.HasPrefix(provided, "Bearer ") {
			provided = strings.TrimSpace(strings.TrimPrefix(provided, "Bearer "))
		} else {
			provided = r.Header.Get("X-XIASS-Proxy-Agent-Token")
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "component": "xiass-proxy-agent"})
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request parseRequest
	if err := readJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	nodes, err := proxyroute.Parse([]byte(request.Input))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	summaries, err := proxyroute.Summaries(nodes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": summaries})
}

func (s *Server) routes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"routes": s.manager.List()})
	case http.MethodPost:
		var request createRequest
		if err := readJSON(r, &request); err != nil || strings.TrimSpace(request.Input) == "" {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		info, err := s.manager.Create(request.Input, request.Index)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "already exists") {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, info)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/routes/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}
	if !s.manager.Delete(id) {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody+1))
	if err != nil || len(data) > maxJSONBody {
		return errors.New("request body is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request body contains trailing data")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
