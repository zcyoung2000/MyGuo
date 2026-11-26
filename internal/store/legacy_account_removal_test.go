package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNewStoreIgnoresOtherAppAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Exercise normal startup, including the remaining old history migration.
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "")
	t.Setenv("MYGUO_DATA_ROOT", "")
	path := filepath.Join(home, "Library", "Application Support", "hgplayer", "account.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"cookies":"sessionid=other","token":"other-token","profile":{"userId":"710000000000088","nickname":"Other account","avatar":"https://example.invalid/avatar.jpg","loggedIn":true}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if profile := s.GetProfile(); profile.LoggedIn || profile.UserID == "710000000000088" || profile.Nickname == "Other account" || profile.Avatar != "" {
		t.Fatalf("another app's account profile was adopted: %+v", profile)
	}
	if _, ok := s.GetAccountAuth(); ok {
		t.Fatal("another app's credentials were adopted")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatal("startup changed another app's account file")
	}
}
