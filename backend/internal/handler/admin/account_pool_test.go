//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountPoolHandlerStub struct {
	service.AdminService
	service.AccountPoolService
	called  bool
	proxyID *int64
}

func (s *accountPoolHandlerStub) SetAccountPoolProxy(_ context.Context, _ int64, id *int64) (*service.AccountPool, error) {
	s.called, s.proxyID = true, id
	return &service.AccountPool{ID: 1, ProxyID: id}, nil
}

func TestAccountPoolProxyHandlerRequiresExplicitIntent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		body   string
		want   int
		called bool
	}{
		{`{}`, 400, false}, {`{"proxy_id":"42"}`, 400, false}, {`{"proxy_id":1.5}`, 400, false},
		{`{"proxy_id":null}`, 200, true}, {`{"proxy_id":42}`, 200, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			s := &accountPoolHandlerStub{}
			h := &AccountHandler{adminService: s}
			r := gin.New()
			r.PUT("/account-pools/:id/proxy", h.SetAccountPoolProxy)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/account-pools/1/proxy", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			require.Equal(t, tc.called, s.called)
		})
	}
}

func TestAccountPoolHandlerInvalidIDDoesNotCallService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, id := range []string{"0", "-1", "invalid"} {
		s := &accountPoolHandlerStub{}
		r := gin.New()
		r.PUT("/account-pools/:id/proxy", (&AccountHandler{adminService: s}).SetAccountPoolProxy)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/account-pools/"+id+"/proxy", strings.NewReader(`{"proxy_id":null}`)))
		require.Equal(t, 400, w.Code)
		require.False(t, s.called)
	}
}
