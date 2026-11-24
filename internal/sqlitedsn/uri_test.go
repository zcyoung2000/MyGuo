package sqlitedsn

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileURLVolumePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive-letter paths are only meaningful on Windows")
	}
	path := filepath.Join("D:"+string(filepath.Separator), "data", "userdata.db")
	want := "file:///D:/data/userdata.db"
	if got := FileURL(path).String(); got != want {
		t.Fatalf("FileURL(%q) = %q, want %q", path, got, want)
	}
}

func TestFileURLSlashPath(t *testing.T) {
	want := "file:///tmp/userdata.db"
	if got := FileURL("/tmp/userdata.db").String(); got != want {
		t.Fatalf("FileURL(/tmp/userdata.db) = %q, want %q", got, want)
	}
}
