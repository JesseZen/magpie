package appdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCallerOnlyDoesNotEnumerateCodexHomes(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex-private")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("synthetic"), 0600)
	t.Setenv("MAGPIE_CODEX_CALLER_ONLY", "1")
	if got := CodexHomesIn(home); len(got) != 0 {
		t.Fatalf("discovered caller homes: %v", got)
	}
}
