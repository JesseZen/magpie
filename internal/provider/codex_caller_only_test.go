//go:build !windows

package provider

import (
	"context"
	"testing"
)

func TestCodexCallerOnlyDoesNotReadOrRefreshLogin(t *testing.T) {
	limitsHome(t)
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	opened := openWatch(t, []string{codexAuthPath()})
	if _, _, err := readCodexLogin(codexHome(), false); err == nil {
		t.Fatal("caller-only mode read the Codex login")
	}
	rememberLogins(true)
	if _, _, err := renewSaved(context.Background(), "codex", "saved@example.com", true, ""); err == nil {
		t.Fatal("caller-only mode allowed a saved Codex token refresh")
	}
	if got := opened(); len(got) != 0 {
		t.Fatalf("opened Codex credentials: %v", got)
	}
}
