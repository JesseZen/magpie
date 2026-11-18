package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/provider"
)

// Exercise Codex's real WebSocket-to-HTTP fallback using a synthetic API key
// and an entirely local official backend.
func TestCodexCallerOnlyNativeClientFallback(t *testing.T) {
	native := os.Getenv("HARBOR_TEST_CODEX_BIN")
	if native == "" {
		t.Skip("set HARBOR_TEST_CODEX_BIN to test a real Codex client")
	}
	fresh(t)
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	var websocketAttempts, officialTurns atomic.Int32
	official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/backend-api/codex/models" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"models":[]}`)
			return
		}
		if r.URL.Path != "/v1/responses" && r.URL.Path != "/backend-api/codex/responses" || r.Method != http.MethodPost {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-fixture" || r.Header.Get("chatgpt-account-id") != "" {
			http.Error(w, "caller identity changed", http.StatusUnauthorized)
			return
		}
		officialTurns.Add(1)
		msg := `{"id":"msg_test","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"HARBOR_HTTP_FALLBACK_OK","annotations":[]}]}`
		response := `{"id":"resp_test","object":"response","status":"completed","model":"gpt-5.5","output":[` + msg + `],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp_test","object":"response","status":"in_progress","output":[]}}`,
			`{"type":"response.output_item.done","output_index":0,"item":` + msg + `}`,
			`{"type":"response.completed","response":` + response + `}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer official.Close()
	oldBase, oldAPIBase := provider.CodexBase, codexAPIBase
	provider.CodexBase, codexAPIBase = official.URL+"/backend-api/codex", official.URL+"/v1"
	defer func() { provider.CodexBase, codexAPIBase = oldBase, oldAPIBase }()
	front := New().Front()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == CodexPath+"/responses" && wantsSocket(r) {
			websocketAttempts.Add(1)
		}
		front.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	root := t.TempDir()
	home := filepath.Join(root, "codex")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"OPENAI_API_KEY":"sk-fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("check_for_update_on_startup = false\ncli_auth_credentials_store = \"file\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, native, "exec", "--ephemeral", "--skip-git-repo-check", "--disable", "apps", "-m", "gpt-5.5", "-c", `openai_base_url="`+gateway.URL+CodexPath+`"`, "Say hello")
	cmd.Dir = root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "CODEX_HOME=" + home, "XDG_CONFIG_HOME=" + root, "XDG_CACHE_HOME=" + root, "TERM=dumb", "LANG=C.UTF-8", "OPENAI_API_KEY=sk-fixture", "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "ALL_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost"}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "HARBOR_HTTP_FALLBACK_OK") {
		t.Fatalf("real Codex failed: %v; socket attempts %d, official turns %d\n%s", err, websocketAttempts.Load(), officialTurns.Load(), out)
	}
	if websocketAttempts.Load() < 1 || officialTurns.Load() < 1 {
		t.Fatalf("expected a WebSocket attempt followed by official HTTP: %d attempts, %d turns", websocketAttempts.Load(), officialTurns.Load())
	}
}
