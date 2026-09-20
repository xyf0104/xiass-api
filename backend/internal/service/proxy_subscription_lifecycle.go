package service

import (
	"context"
	"time"
)

func (s *ProxySubscriptionService) Start() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	done := s.done
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		nextRefresh := time.Now().Add(30 * time.Minute)
		for {
			// Every instance restores only its own loopback engine from the same
			// encrypted snapshot; routine recovery never rewrites shared proxies.
			_ = s.reconcile(ctx)
			if executionNodeControlPlaneEnabled(s.cfg) && time.Now().After(nextRefresh) {
				_, _ = s.Refresh(ctx)
				nextRefresh = time.Now().Add(30 * time.Minute)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *ProxySubscriptionService) reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, preview := range s.previews {
		if time.Now().After(preview.Expires) {
			delete(s.previews, id)
		}
	}
	c, raw, err := s.load(ctx)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	_, err = s.agent.Sync(ctx, syncSubscriptionRequest(c, true))
	if err != nil {
		s.lastError = "Saved subscription listeners are not available on this server"
	} else if s.lastError == "Saved subscription listeners are not available on this server" {
		s.lastError = ""
	}
	return err
}

func (s *ProxySubscriptionService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	cancel, done := s.cancel, s.done
	s.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if agent, ok := s.agent.(interface{ Close() }); ok {
		agent.Close()
	}
}
