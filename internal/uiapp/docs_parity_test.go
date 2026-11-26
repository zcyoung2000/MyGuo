package uiapp

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestSettingsParityDocsStayCurrent(t *testing.T) {
	parity, err := os.ReadFile("../../doc/architecture/native-feature-parity.md")
	if err != nil {
		t.Fatalf("read native feature parity document: %v", err)
	}
	settingsRow := regexp.MustCompile(`(?m)^\| Settings \|.*\|\s*$`).FindString(string(parity))
	if settingsRow == "" {
		t.Fatal("native feature parity document has no Settings row")
	}
	if !strings.Contains(settingsRow, "renderSettings") {
		t.Fatal("Settings parity row does not identify the native renderer")
	}
	if regexp.MustCompile(`(?i)\babout\b\s*\|`).MatchString(settingsRow) {
		t.Fatal("Settings parity row lists the removed about feature")
	}
}
