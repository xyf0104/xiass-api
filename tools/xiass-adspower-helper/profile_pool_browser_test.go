package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestRecycledProfileBrowserContextsDoNotShareAccountStorage(t *testing.T) {
	chrome := os.Getenv("XIASS_TEST_CHROME")
	if chrome == "" {
		t.Skip("set XIASS_TEST_CHROME for isolated browser-context fixture")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><html><body>Local OAuth isolation fixture</body></html>"))
	}))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dir := t.TempDir()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome), chromedp.UserDataDir(dir))
	allocator, closeAllocator := chromedp.NewExecAllocator(ctx, opts...)
	defer closeAllocator()
	root, closeRoot := chromedp.NewContext(allocator)
	defer closeRoot()
	require.NoError(t, chromedp.Run(root, chromedp.Navigate(fixture.URL), chromedp.Evaluate(`document.cookie='account=original;path=/';localStorage.setItem('account','original')`, nil)))
	portFile, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	require.NoError(t, err)
	parts := strings.Split(strings.TrimSpace(string(portFile)), "\n")
	require.Len(t, parts, 2)
	endpoint := "ws://127.0.0.1:" + parts[0] + parts[1]
	for _, identity := range []string{"second", "third"} {
		browser, closeBrowser, err := openAdsPowerOAuthTargetWithIsolation(ctx, endpoint, fixture.URL, true)
		require.NoError(t, err)
		state := chromedp.FromContext(browser)
		info, err := target.GetTargetInfo().WithTargetID(state.Target.TargetID).Do(cdp.WithExecutor(browser, state.Browser))
		require.NoError(t, err)
		require.NotEmpty(t, info.BrowserContextID)
		var values []string
		require.NoError(t, chromedp.Run(browser, chromedp.Evaluate(`[document.cookie,localStorage.getItem('account')||'']`, &values)))
		require.Equal(t, []string{"", ""}, values)
		require.NoError(t, chromedp.Run(browser, chromedp.Evaluate(`document.cookie='account=`+identity+`;path=/';localStorage.setItem('account','`+identity+`')`, nil)))
		closeBrowser()
	}
}
