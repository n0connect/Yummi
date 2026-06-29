package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/n0connect/yummi/internal/config"
)

// sessionFilePath is where the TUI remembers its last session id across
// launches, mirroring the orchestrator's own ~/.yummi convention.
func sessionFilePath() string {
	return filepath.Join(config.HomeDir(), "tui_session_id")
}

// loadPersistedSessionID returns the last-used session id, or "" if none was
// saved or it can't be read — either case just falls back to creating a new
// session, never an error worth surfacing to the operator.
func loadPersistedSessionID() string {
	b, err := os.ReadFile(sessionFilePath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// savePersistedSessionID best-effort writes id for the next launch to reuse.
func savePersistedSessionID(id string) {
	path := sessionFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(id), 0o600)
}
