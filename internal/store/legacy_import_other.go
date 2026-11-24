//go:build !windows

package store

import (
	"encoding/json"
	"os/exec"
)

// legacyProgressRows keeps the original macOS sqlite3 CLI dump unchanged.
func legacyProgressRows(dbPath string) ([]map[string]any, error) {
	cmd := exec.Command("/usr/bin/sqlite3", "-json", dbPath, legacyProgressQuery+";")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var rawRows []map[string]any
	if err := json.Unmarshal(out, &rawRows); err != nil {
		return nil, err
	}
	return rawRows, nil
}
