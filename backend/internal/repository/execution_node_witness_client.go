package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const executionNodeWitnessMaxResponseBytes = 32 * 1024

type executionNodeWitnessClient struct {
	baseURL *url.URL
	token   string
	client  *http.Client
}

type executionNodeWitnessRequest struct {
	ClusterID  string `json:"cluster_id"`
	NodeID     string `json:"node_id"`
	LeaseID    string `json:"lease_id"`
	Generation int64  `json:"generation,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type executionNodeWitnessResponse struct {
	service.ExecutionNodeWitnessLease
	Acquired    bool  `json:"acquired"`
	Renewed     bool  `json:"renewed"`
	Released    bool  `json:"released"`
	RemainingMS int64 `json:"remaining_ms"`
}

func NewExecutionNodeWitnessClient(cfg *config.Config) service.ExecutionNodeWitnessClient {
	if cfg == nil || !cfg.Gateway.ExecutionNode.Witness.Enabled {
		return nil
	}
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.Gateway.ExecutionNode.Witness.URL), "/"))
	if err != nil || parsed == nil {
		return nil
	}
	timeout := time.Duration(cfg.Gateway.ExecutionNode.Witness.RequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &executionNodeWitnessClient{
		baseURL: parsed,
		token:   strings.TrimSpace(cfg.Gateway.ExecutionNode.Witness.Token),
		client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (c *executionNodeWitnessClient) Acquire(ctx context.Context, clusterID, nodeID, leaseID string, ttl time.Duration) (service.ExecutionNodeWitnessLease, bool, error) {
	response, status, err := c.do(ctx, http.MethodPost, "/v1/lease/acquire", executionNodeWitnessRequest{
		ClusterID: clusterID, NodeID: nodeID, LeaseID: leaseID, TTLSeconds: int(ttl / time.Second),
	})
	if err != nil {
		return service.ExecutionNodeWitnessLease{}, false, err
	}
	return response.ExecutionNodeWitnessLease, status == http.StatusOK && response.Acquired, nil
}

func (c *executionNodeWitnessClient) Renew(ctx context.Context, lease service.ExecutionNodeWitnessLease, ttl time.Duration) (service.ExecutionNodeWitnessLease, bool, error) {
	response, status, err := c.do(ctx, http.MethodPost, "/v1/lease/renew", executionNodeWitnessRequest{
		ClusterID: lease.ClusterID, NodeID: lease.HolderNodeID, LeaseID: lease.LeaseID,
		Generation: lease.Generation, TTLSeconds: int(ttl / time.Second),
	})
	if err != nil {
		return service.ExecutionNodeWitnessLease{}, false, err
	}
	return response.ExecutionNodeWitnessLease, status == http.StatusOK && response.Renewed, nil
}

func (c *executionNodeWitnessClient) Release(ctx context.Context, lease service.ExecutionNodeWitnessLease) (service.ExecutionNodeWitnessLease, bool, error) {
	response, status, err := c.do(ctx, http.MethodPost, "/v1/lease/release", executionNodeWitnessRequest{
		ClusterID: lease.ClusterID, NodeID: lease.HolderNodeID, LeaseID: lease.LeaseID, Generation: lease.Generation,
	})
	if err != nil {
		return service.ExecutionNodeWitnessLease{}, false, err
	}
	return response.ExecutionNodeWitnessLease, status == http.StatusOK && response.Released, nil
}

func (c *executionNodeWitnessClient) Status(ctx context.Context, clusterID string) (service.ExecutionNodeWitnessLease, error) {
	path := "/v1/lease/status?cluster_id=" + url.QueryEscape(clusterID)
	response, _, err := c.do(ctx, http.MethodGet, path, nil)
	return response.ExecutionNodeWitnessLease, err
}

func (c *executionNodeWitnessClient) do(ctx context.Context, method, path string, payload any) (executionNodeWitnessResponse, int, error) {
	if c == nil || c.baseURL == nil || c.client == nil {
		return executionNodeWitnessResponse{}, 0, fmt.Errorf("execution-node witness client is unavailable")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return executionNodeWitnessResponse{}, 0, err
		}
		body = bytes.NewReader(encoded)
	}
	target := *c.baseURL
	target.Path = strings.TrimRight(c.baseURL.Path, "/") + strings.SplitN(path, "?", 2)[0]
	if parts := strings.SplitN(path, "?", 2); len(parts) == 2 {
		target.RawQuery = parts[1]
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return executionNodeWitnessResponse{}, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return executionNodeWitnessResponse{}, 0, fmt.Errorf("contact execution-node witness: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, executionNodeWitnessMaxResponseBytes))
		return executionNodeWitnessResponse{}, resp.StatusCode, fmt.Errorf("execution-node witness returned status %d", resp.StatusCode)
	}
	var result executionNodeWitnessResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, executionNodeWitnessMaxResponseBytes))
	if err := decoder.Decode(&result); err != nil {
		return executionNodeWitnessResponse{}, resp.StatusCode, fmt.Errorf("decode execution-node witness response: %w", err)
	}
	if result.Active {
		if result.RemainingMS <= 0 || result.RemainingMS > 60000 {
			return executionNodeWitnessResponse{}, resp.StatusCode, fmt.Errorf("invalid execution-node witness lease duration")
		}
		// Charge the whole round trip against the lease, never extend it by network delay.
		result.Remaining = time.Duration(result.RemainingMS)*time.Millisecond - time.Since(started)
		if result.Remaining <= 0 {
			return executionNodeWitnessResponse{}, resp.StatusCode, fmt.Errorf("execution-node witness lease expired in transit")
		}
	}
	return result, resp.StatusCode, nil
}
