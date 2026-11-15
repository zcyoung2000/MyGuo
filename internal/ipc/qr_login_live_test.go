package ipc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in interactive probe. It uses a fresh SQLite directory and never reads
// installed credentials, sends comments, or uploads playback progress.
func TestLiveQRLogin(t *testing.T) {
	if os.Getenv("MYGUO_LIVE_QR") != "1" {
		t.Skip("set MYGUO_LIVE_QR=1 for interactive phone confirmation")
	}
	dir := os.Getenv("MYGUO_QR_PROBE_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := newHongguoService(nil, db, &MediaServer{}, dir, dir)
	defer CloseService(s)
	s.httpClient = &http.Client{Timeout: 12 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	code, err := s.BeginQRLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "login-qr.png")
	if err = os.WriteFile(path, code.Image, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("QR_READY path=%s remainingSeconds=%d", path, code.ExpiresAt-time.Now().Unix())
	previous := ""
	for time.Now().Unix() < code.ExpiresAt {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
		result, e := s.PollQRLogin(ctx, code.ID)
		if e != nil {
			t.Fatal(e)
		}
		if result.State != previous {
			t.Logf("QR_STATE %s", result.State)
			previous = result.State
		}
		if result.State == "confirmed" {
			if result.Profile == nil || !result.Profile.LoggedIn {
				t.Fatal("invalid confirmed profile")
			}
			auth, ok := db.GetAccountAuth()
			if !ok || auth.Cookies == "" {
				t.Fatal("session not persisted")
			}
			t.Log("QR_CONFIRMED profile authenticated and persisted; no credentials logged")
			return
		}
	}
	t.Fatal("QR expired before phone confirmation")
}
