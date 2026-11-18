package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCallerOnlyNeverWritesOrDeletesProfileLogin(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "auth.json")
	os.WriteFile(path, []byte("synthetic-profile-login"), 0600)
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	if err := saveCodexLogin(home, []byte("replacement")); err == nil {
		t.Fatal("write allowed")
	}
	if err := deleteCodexLogin(home); err == nil {
		t.Fatal("delete allowed")
	}
	if _, _, err := readCodexLogin(home, true); err == nil {
		t.Fatal("read allowed")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "synthetic-profile-login" {
		t.Fatalf("login changed: %v", err)
	}
}
