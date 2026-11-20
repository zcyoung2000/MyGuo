package hgapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func normalizeArtStyleCursor(token string, selections map[string][]string) (string, error) {
	if !strings.HasPrefix(token, "art1.") {
		return token, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "art1."))
	var saved struct {
		Base  string   `json:"cursor"`
		Style []string `json:"style"`
	}
	if err != nil || json.Unmarshal(raw, &saved) != nil || saved.Base == "" || strings.Join(saved.Style, "\x00") != strings.Join(selections["category_dim_art_style"], "\x00") {
		return "", fmt.Errorf("cursor belongs to a different art-style filter")
	}
	return saved.Base, nil
}
