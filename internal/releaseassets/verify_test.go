package releaseassets

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func signedFixture(t *testing.T) (string, Config, ed25519.PrivateKey, manifest) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := Config{Slug: "myguo", Version: "0.1.6"}
	c.Updates.GitHub, c.Updates.PublicKey = "zcyoung2000/MyGuo", base64.StdEncoding.EncodeToString(pub)
	dir := t.TempDir()
	archive := []byte("signed fixture archive")
	digest := sha256.Sum256(archive)
	m := manifest{Version: c.Version, Size: int64(len(archive)),
		URL:       "https://github.com/zcyoung2000/MyGuo/releases/download/v0.1.6/myguo-0.1.6-darwin-arm64.tar.gz",
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, digest[:])),
	}
	writeFixture(t, dir, "myguo-0.1.6-darwin-arm64.tar.gz", archive)
	writeFixture(t, dir, "myguo-0.1.6-arm64.dmg", []byte("installer fixture"))
	writeManifest(t, dir, m)
	return dir, c, priv, m
}

func writeFixture(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, dir string, m manifest) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "update-darwin-arm64.json", data)
}

func TestVerifyReleaseRejectsUntrustedOrIncompleteUpdates(t *testing.T) {
	for _, scenario := range []string{"valid", "changed archive", "wrong key", "missing target", "missing installer", "old version", "foreign URL", "invalid signature", "wrong size"} {
		t.Run(scenario, func(t *testing.T) {
			dir, c, _, m := signedFixture(t)
			targets := []string{"darwin-arm64"}
			switch scenario {
			case "changed archive":
				writeFixture(t, dir, "myguo-0.1.6-darwin-arm64.tar.gz", []byte("SIGNED fixture archive"))
			case "wrong key":
				pub, _, _ := ed25519.GenerateKey(rand.Reader)
				c.Updates.PublicKey = base64.StdEncoding.EncodeToString(pub)
			case "missing target":
				targets = append(targets, "darwin-amd64")
			case "missing installer":
				if err := os.Remove(filepath.Join(dir, "myguo-0.1.6-arm64.dmg")); err != nil {
					t.Fatal(err)
				}
			case "old version":
				m.Version = "0.1.5"
			case "foreign URL":
				m.URL = "https://example.com/myguo-0.1.6-darwin-arm64.tar.gz"
			case "invalid signature":
				m.Signature = "not-base64"
			case "wrong size":
				m.Size++
			}
			writeManifest(t, dir, m)
			err := Verify(dir, c, targets)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("verification result: %v", err)
			}
		})
	}
}
