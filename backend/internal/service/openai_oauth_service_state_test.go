package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type openaiOAuthClientStateStub struct {
	exchangeCalled int32
	lastClientID   string
}

type retainingPublicSessionStore struct {
	sessions map[string]*openai.OAuthSession
}

func newRetainingPublicSessionStore() *retainingPublicSessionStore {
	return &retainingPublicSessionStore{sessions: make(map[string]*openai.OAuthSession)}
}

func (s *retainingPublicSessionStore) Store(_ context.Context, sessionID string, session *openai.OAuthSession) error {
	if strings.TrimSpace(sessionID) == "" || session == nil {
		return errors.New("invalid public oauth session")
	}
	s.sessions[sessionID] = session
	return nil
}

func (s *retainingPublicSessionStore) Consume(_ context.Context, sessionID, state, browserBindingHash string) (*openai.OAuthSession, bool, error) {
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false, nil
	}
	if state != session.State {
		return nil, false, ErrPublicOpenAIOAuthStateMismatch
	}
	if session.BrowserBindingHash != "" && strings.TrimSpace(browserBindingHash) != session.BrowserBindingHash {
		return nil, false, ErrPublicOpenAIOAuthBrowserBindingMismatch
	}
	delete(s.sessions, sessionID)
	return session, true, nil
}

func (s *openaiOAuthClientStateStub) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.exchangeCalled, 1)
	s.lastClientID = clientID
	return &openai.TokenResponse{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresIn:    3600,
	}, nil
}

func (s *openaiOAuthClientStateStub) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *openaiOAuthClientStateStub) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string) (*openai.TokenResponse, error) {
	return s.RefreshToken(ctx, refreshToken, proxyURL)
}

func TestOpenAIOAuthService_ExchangeCode_StateRequired(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()

	svc.sessionStore.Set("sid", &openai.OAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "oauth state is required")
	require.Equal(t, int32(0), atomic.LoadInt32(&client.exchangeCalled))
}

func TestOpenAIOAuthService_ExchangeCode_StateMismatch(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()

	svc.sessionStore.Set("sid", &openai.OAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
		State:     "wrong-state",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid oauth state")
	require.Equal(t, int32(0), atomic.LoadInt32(&client.exchangeCalled))
}

func TestOpenAIOAuthService_ExchangeCode_StateMatch(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()

	svc.sessionStore.Set("sid", &openai.OAuthSession{
		State:        "expected-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		CreatedAt:    time.Now(),
	})

	info, err := svc.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: "sid",
		Code:      "auth-code",
		State:     "expected-state",
	})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "at", info.AccessToken)
	require.Equal(t, openai.ClientID, info.ClientID)
	require.Equal(t, openai.ClientID, client.lastClientID)
	require.Equal(t, int32(1), atomic.LoadInt32(&client.exchangeCalled))

	_, ok := svc.sessionStore.Get("sid")
	require.False(t, ok)
}

func TestOpenAIOAuthService_ExchangeWorkflowCodeRetainsSessionUntilRevoked(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()

	svc.sessionStore.Set("workflow-sid", &openai.OAuthSession{
		State:        "workflow-state",
		CodeVerifier: "verifier",
		RedirectURI:  openai.DefaultRedirectURI,
		ProxyURL:     "socks5://proxy.example:1080",
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeWorkflowCode(context.Background(), &OpenAIExchangeCodeInput{
		SessionID: "workflow-sid",
		Code:      "auth-code",
		State:     "workflow-state",
	})
	require.NoError(t, err)

	_, ok := svc.sessionStore.Get("workflow-sid")
	require.True(t, ok, "workflow callers must enrich through the selected proxy before revoking the session")

	svc.RevokeWorkflowSession("workflow-sid")
	_, ok = svc.sessionStore.Get("workflow-sid")
	require.False(t, ok)
}

func TestOpenAIOAuthService_ExchangeCodeForPublicToolSkipsEnrichment(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	var privacyFactoryCalled int32
	svc := NewOpenAIOAuthService(nil, client)
	publicSessions := newRetainingPublicSessionStore()
	svc.SetPublicSessionStore(publicSessions)
	svc.SetPrivacyClientFactory(func(_ string) (*req.Client, error) {
		atomic.AddInt32(&privacyFactoryCalled, 1)
		return nil, errors.New("public tool must not create a privacy client")
	})
	defer svc.Stop()

	require.NoError(t, publicSessions.Store(context.Background(), "public-sid", &openai.OAuthSession{
		State:              "public-state",
		CodeVerifier:       "verifier",
		RedirectURI:        openai.DefaultRedirectURI,
		BrowserBindingHash: "browser-hash",
		CreatedAt:          time.Now(),
	}))

	info, err := svc.ExchangeCodeForPublicTool(context.Background(), &OpenAIExchangeCodeInput{
		SessionID:          "public-sid",
		Code:               "auth-code",
		State:              "public-state",
		BrowserBindingHash: "browser-hash",
	})
	require.NoError(t, err)
	require.Equal(t, "at", info.AccessToken)
	require.Equal(t, "rt", info.RefreshToken)
	require.Equal(t, int32(0), atomic.LoadInt32(&privacyFactoryCalled))

	_, err = svc.ExchangeCodeForPublicTool(context.Background(), &OpenAIExchangeCodeInput{
		SessionID:          "public-sid",
		Code:               "auth-code",
		State:              "public-state",
		BrowserBindingHash: "browser-hash",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found or expired")
	require.Equal(t, int32(1), atomic.LoadInt32(&client.exchangeCalled))
}

func TestOpenAIOAuthService_PublicToolKeepsSessionAfterInvalidProof(t *testing.T) {
	client := &openaiOAuthClientStateStub{}
	svc := NewOpenAIOAuthService(nil, client)
	publicSessions := newRetainingPublicSessionStore()
	svc.SetPublicSessionStore(publicSessions)
	defer svc.Stop()

	storeSession := func(id string) {
		require.NoError(t, publicSessions.Store(context.Background(), id, &openai.OAuthSession{
			State:              "expected-state",
			CodeVerifier:       "verifier",
			RedirectURI:        openai.DefaultRedirectURI,
			BrowserBindingHash: "expected-browser",
			CreatedAt:          time.Now(),
		}))
	}

	storeSession("wrong-state")
	_, err := svc.ExchangeCodeForPublicTool(context.Background(), &OpenAIExchangeCodeInput{
		SessionID:          "wrong-state",
		Code:               "auth-code",
		State:              "incorrect-state",
		BrowserBindingHash: "expected-browser",
	})
	require.ErrorContains(t, err, "invalid oauth state")
	info, err := svc.ExchangeCodeForPublicTool(context.Background(), &OpenAIExchangeCodeInput{
		SessionID:          "wrong-state",
		Code:               "auth-code",
		State:              "expected-state",
		BrowserBindingHash: "expected-browser",
	})
	require.NoError(t, err)
	require.Equal(t, "at", info.AccessToken)

	storeSession("wrong-browser")
	_, err = svc.ExchangeCodeForPublicTool(context.Background(), &OpenAIExchangeCodeInput{
		SessionID:          "wrong-browser",
		Code:               "auth-code",
		State:              "expected-state",
		BrowserBindingHash: "incorrect-browser",
	})
	require.ErrorContains(t, err, "different browser")
	info, err = svc.ExchangeCodeForPublicTool(context.Background(), &OpenAIExchangeCodeInput{
		SessionID:          "wrong-browser",
		Code:               "auth-code",
		State:              "expected-state",
		BrowserBindingHash: "expected-browser",
	})
	require.NoError(t, err)
	require.Equal(t, "at", info.AccessToken)
	require.Equal(t, int32(2), atomic.LoadInt32(&client.exchangeCalled))
}
