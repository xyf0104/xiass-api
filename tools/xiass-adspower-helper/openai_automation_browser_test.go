package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestOAuthPhoneReplacementInBrowser(t *testing.T) {
	chrome := os.Getenv("XIASS_TEST_CHROME")
	if chrome == "" {
		t.Skip("set XIASS_TEST_CHROME to run the isolated real-browser OAuth fixture")
	}
	var mu sync.Mutex
	var actions []string
	changes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/add-phone" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(phoneReplacementFixture))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var request map[string]string
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/api/v1/tools/adspower/progress/report" {
			_ = json.NewEncoder(w).Encode(apiEnvelope[map[string]any]{Data: map[string]any{"accepted": true}})
			return
		}
		if r.URL.Path != "/api/v1/tools/adspower/sms/action" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		action := request["action"]
		actions = append(actions, action)
		result := adsPowerSMSActionResult{Status: "WAITING"}
		switch action {
		case "acquire":
			result.Number = "+12605550101"
		case "change":
			changes++
			switch changes {
			case 1:
				// The page clears its error while the provider request fails.
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(apiEnvelope[adsPowerSMSActionResult]{Code: 502})
				return
			case 2:
				result.Number = "+12605550101"
			case 3:
				result.Number = "+12605550102"
			default:
				result.Number = "+12605550103"
			}
		case "check":
			result.Status, result.Code = "RECEIVED", "654321"
		}
		_ = json.NewEncoder(w).Encode(apiEnvelope[adsPowerSMSActionResult]{Data: result})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome))
	allocator, closeAllocator := chromedp.NewExecAllocator(ctx, opts...)
	defer closeAllocator()
	browser, closeBrowser := chromedp.NewContext(allocator)
	defer closeBrowser()
	require.NoError(t, chromedp.Run(browser, chromedp.Navigate(server.URL+"/add-phone")))
	helper := &helperServer{client: server.Client()}
	err := helper.automateOpenAI(ctx, browser, server.URL, &launchPayload{
		LoginEmail: "fixture@example.test", CallbackToken: "fixture-callback-token",
	})
	// The fixture deliberately ends instead of creating an account or token.
	require.Equal(t, "oauth_session_expired", automationFailure(err).Reason)
	var observed struct {
		Numbers   []string `json:"numbers"`
		Channels  []string `json:"channels"`
		Code      string   `json:"code"`
		Workspace bool     `json:"workspace"`
	}
	require.NoError(t, chromedp.Run(browser, chromedp.Evaluate("window.observed", &observed)))
	require.Equal(t, []string{"+12605550101", "+12605550102", "+12605550103"}, observed.Numbers)
	require.Equal(t, []string{"sms", "sms", "sms"}, observed.Channels)
	require.Equal(t, "654321", observed.Code)
	require.True(t, observed.Workspace)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"acquire", "change", "change", "change", "change", "check"}, actions)
}

const phoneReplacementFixture = `<!doctype html><html><body>
<h1>Phone number required</h1>
<form id="phoneForm">
  <input type="tel" name="phone" id="phone">
  <label><input type="radio" name="channel" value="sms">Text message</label>
  <label><input type="radio" name="channel" value="whatsapp" checked>WhatsApp</label>
  <div id="error"></div><button type="submit" disabled>Continue</button>
</form>
<script>
window.observed={numbers:[],channels:[],code:'',workspace:false};
const form=document.querySelector('form');
document.querySelector('#phone').addEventListener('input',()=>{
  form.querySelector('button').disabled=!document.querySelector('#phone').value;
});
form.addEventListener('submit',e=>{
  e.preventDefault();
  observed.numbers.push(document.querySelector('#phone').value);
  observed.channels.push(document.querySelector('input[name="channel"]:checked').value);
  const button=form.querySelector('button');button.disabled=true;
  // Keep the previous rejection visible throughout the next request.
  setTimeout(()=>{
    button.disabled=false;
    const count=observed.numbers.length;
    if(count<3){
      document.querySelector('#error').textContent=count===1
        ? 'This phone number is not supported.'
        : 'This phone number is already associated with another account.';
      if(count===1)setTimeout(()=>document.querySelector('#error').textContent='',1500);
      return;
    }
    history.replaceState(null,'','/phone-verification');
    document.body.innerHTML='<h1>Enter the text message code sent to your phone</h1><form><input name="code" autocomplete="one-time-code"><button>Verify</button></form>';
    document.querySelector('form').onsubmit=e=>{
      e.preventDefault();observed.code=document.querySelector('input').value;
      history.replaceState(null,'','/consent');
      document.body.innerHTML='<h1>Continue to Codex using your workspace</h1><button id="next">Next</button>';
      document.querySelector('#next').onclick=()=>{
        observed.workspace=true;document.body.innerHTML='<h1>Authentication Error error_code: invalid_state</h1>';
      };
    };
  },4000);
});
</script></body></html>`
