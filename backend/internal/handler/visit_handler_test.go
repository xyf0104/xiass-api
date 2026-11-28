//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type visitRecorderStub struct {
	entries []*service.AuditLog
}

func (r *visitRecorderStub) Record(entry *service.AuditLog) {
	r.entries = append(r.entries, entry)
}

func TestVisitHandlerRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, loggedIn := range []bool{false, true} {
		t.Run(map[bool]string{false: "anonymous", true: "authenticated"}[loggedIn], func(t *testing.T) {
			recorder := &visitRecorderStub{}
			h := &VisitHandler{auditService: recorder}
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(nil))
			r.POST("/visits", func(c *gin.Context) {
				if loggedIn {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
					c.Set(middleware.ContextKeyAuthEmail, "verified@example.com")
					c.Set(string(middleware.ContextKeyUserRole), service.RoleUser)
				}
				c.Next()
			}, h.Record)
			req := httptest.NewRequest(http.MethodPost, "/visits", strings.NewReader(`{"path":"/pricing?token=secret#hash","actor_email":"forged@example.com","client_ip":"1.1.1.1"}`))
			req.RemoteAddr = "192.0.2.10:1234"
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "Visitor test")
			req.Header.Set("X-Forwarded-For", "198.51.100.9")
			req.Header.Set("CF-Connecting-IP", "198.51.100.9")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code)
			require.Len(t, recorder.entries, 1)
			entry := recorder.entries[0]
			require.Equal(t, service.AuditActionSiteVisit, entry.Action)
			require.Equal(t, "/pricing", entry.Path)
			require.Equal(t, "192.0.2.10", entry.ClientIP)
			require.Equal(t, "Visitor test", entry.UserAgent)
			require.Empty(t, entry.RequestBody)
			require.Empty(t, entry.CredentialMasked)
			if loggedIn {
				require.EqualValues(t, 42, *entry.ActorUserID)
				require.Equal(t, "verified@example.com", entry.ActorEmail)
				require.Equal(t, service.AuditAuthMethodJWT, entry.AuthMethod)
			} else {
				require.Nil(t, entry.ActorUserID)
				require.Empty(t, entry.ActorEmail)
			}
		})
	}
}

func TestVisitHandlerTrustedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &visitRecorderStub{}
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies([]string{"172.18.0.1/32"}))
	r.POST("/visits", (&VisitHandler{auditService: recorder}).Record)
	req := httptest.NewRequest(http.MethodPost, "/visits", strings.NewReader(`{"path":"/home"}`))
	req.RemoteAddr = "172.18.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.8, 203.0.113.9")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "203.0.113.9", recorder.entries[0].ClientIP)
}

func TestVisitHandlerInvalidPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{}`, `{"path":"https://example.com"}`, `{"path":"//example.com"}`,
		`{"path":"relative"}`, `{"path":"/%xx"}`, `{"path":"` + strings.Repeat("a", 2049) + `"}`,
	} {
		recorder := &visitRecorderStub{}
		r := gin.New()
		r.POST("/visits", (&VisitHandler{auditService: recorder}).Record)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/visits", strings.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		require.Empty(t, recorder.entries)
	}
}
