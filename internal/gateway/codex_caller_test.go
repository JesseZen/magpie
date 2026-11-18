package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

// A Codex caller may opt out of account rotation without disabling
// third-party providers or changing Magpie's default routing behavior.
func TestCodexCallerOnlyDoesNotSwitchAccounts(t *testing.T) {
	codexSignedIn(t, "spare@example.com")
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	var tried, models []string
	usedUp(t, &tried, &models)

	code, body := codexPost(t, `{"model":"gpt-5.5","stream":true,"input":"ping"}`)
	if code != http.StatusTooManyRequests || !strings.Contains(body, "usage_limit_reached") {
		t.Fatalf("caller should receive its own account's limit: %d %s (accounts %v)", code, body, tried)
	}
	if got := strings.Join(tried, ","); got != "acct-1" {
		t.Fatalf("requested accounts %s; want only the caller", got)
	}
}

func TestCodexCallerOnlyRelaysEachCallerAndCompaction(t *testing.T) {
	codexSignedIn(t, "spare@example.com")
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	var mu sync.Mutex
	got := map[string]string{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.Header.Get("chatgpt-account-id")+r.URL.Path] = r.Header.Get("Authorization") + " " + string(body)
		mu.Unlock()
		w.Header().Set("X-Test-Official", "original")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"caller token expired"}`)
	}))
	defer up.Close()
	was := provider.CodexBase
	provider.CodexBase = up.URL + "/backend-api/codex"
	defer func() { provider.CodexBase = was }()
	server := New().Handler()
	body := `{"model":"gpt-5.5","stream":true,"input":"ping","unknown_official_field":7}`
	var wg sync.WaitGroup
	for _, id := range []string{"A", "B"} {
		for _, endpoint := range []string{"/responses", "/responses/compact"} {
			wg.Add(1)
			go func(id, endpoint string) {
				defer wg.Done()
				req := httptest.NewRequest("POST", CodexPath+endpoint, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer caller-"+id)
				req.Header.Set("chatgpt-account-id", id)
				rec := httptest.NewRecorder()
				server.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized || rec.Header().Get("X-Test-Official") != "original" || rec.Body.String() != `{"error":"caller token expired"}` {
					t.Errorf("caller %s %s: %d %s", id, endpoint, rec.Code, rec.Body.String())
				}
			}(id, endpoint)
		}
	}
	wg.Wait()
	for _, id := range []string{"A", "B"} {
		for _, endpoint := range []string{"/responses", "/responses/compact"} {
			if v := got[id+CodexPath+endpoint]; v != "Bearer caller-"+id+" "+body {
				t.Errorf("caller %s %s got %q", id, endpoint, v)
			}
		}
	}
}

func TestCodexCallerOnlyThirdPartyKeepsItsOwnKey(t *testing.T) {
	f := &fake{t: t, reply: sse(`data: {"type":"response.completed","response":{"id":"r1","model":"m1","output":[]}}`)}
	setup(t, provider.Responses, f)
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	if err := provider.SaveGroup(provider.Group{ID: "g", Name: "G", Members: []string{"fake/m1"}}); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"fake/m1", "group/g"} {
		code, body := codexPost(t, `{"model":"`+model+`","stream":true,"input":"ping"}`)
		if code != http.StatusOK || !strings.Contains(body, "response.completed") {
			t.Fatalf("%s: status %d: %s", model, code, body)
		}
		if f.head.Get("Authorization") != "Bearer k" || f.head.Get("chatgpt-account-id") != "" {
			t.Fatalf("%s: third-party credentials are incorrect", model)
		}
	}
}

// Let Codex use its own HTTP transport, so official turns do not pass through
// Magpie's WebSocket-to-HTTP conversation reconstruction.
func TestCodexCallerOnlyUsesNativeHTTPFallback(t *testing.T) {
	fresh(t)
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	_, ts := sockServer(t)
	_, resp, err := dialSock(t, ts, CodexPath+"/responses", nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("Codex WebSocket upgrade should trigger its HTTP fallback: status %v, error %v", resp, err)
	}
	// Other OpenAI-compatible clients retain their existing socket behavior.
	_, resp, err = dialSock(t, ts, "/v1/responses", nil)
	if err != nil {
		t.Fatalf("ordinary Responses socket should still work: status %v, error %v", resp, err)
	}
}

func TestCodexCallerOnlyBypassesMagpieTransport(t *testing.T) {
	fresh(t)
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: official\n\n")
	}))
	defer official.Close()
	old := provider.CodexBase
	provider.CodexBase = official.URL + "/backend-api/codex"
	defer func() { provider.CodexBase = old }()
	server := New()
	server.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("official request reached Magpie's translating transport")
		return nil, io.EOF
	})}
	r := httptest.NewRequest(http.MethodPost, CodexPath+"/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true}`))
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "data: official\n\n" {
		t.Fatalf("official response not relayed: %d %s", w.Code, w.Body.String())
	}
}
