// Package releaseassets validates signed releases before they are published.
package releaseassets

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/pe"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Slug    string
	Version string
	Updates struct {
		GitHub    string
		PublicKey string
	}
}

type manifest struct {
	Version   string
	URL       string
	Size      int64
	Signature string
	Deltas    []struct {
		URL       string
		Size      int64
		Signature string
	}
}

// Verify checks every target's manifest, Ed25519 archive/delta signatures,
// and installer presence. Missing assets fail rather than publishing a
// release that strands clients reading /releases/latest/download/.
func Verify(dir string, config Config, targets []string) error {
	key, err := base64.StdEncoding.DecodeString(config.Updates.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid update public key")
	}
	if config.Slug == "" || config.Version == "" || config.Updates.GitHub == "" || len(targets) == 0 {
		return fmt.Errorf("release configuration is incomplete")
	}
	base := "https://github.com/" + config.Updates.GitHub + "/releases/download/v" + config.Version + "/"
	for _, target := range targets {
		name := config.Slug + "-" + config.Version + "-" + target + ".tar.gz"
		data, err := os.ReadFile(filepath.Join(dir, "update-"+target+".json"))
		if err != nil {
			return err
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		if m.Version != config.Version || m.URL != base+name {
			return fmt.Errorf("%s: manifest version or archive URL does not match release", target)
		}
		if err := verifySigned(dir, name, m.Size, m.Signature, key); err != nil {
			return err
		}
		for _, d := range m.Deltas {
			name, ok := strings.CutPrefix(d.URL, base)
			if !ok || filepath.Base(name) != name || strings.ContainsAny(name, "\\?#") || !strings.HasSuffix(name, "-"+target+".delta") {
				return fmt.Errorf("%s: invalid delta URL", target)
			}
			if err := verifySigned(dir, name, d.Size, d.Signature, key); err != nil {
				return err
			}
		}
		var installer string
		switch target {
		case "darwin-arm64", "darwin-amd64":
			installer = config.Slug + "-" + config.Version + "-" + strings.TrimPrefix(target, "darwin-") + ".dmg"
		case "windows-amd64":
			installer = config.Slug + "-setup-" + config.Version + "-amd64.exe"
			if err := verifyWindowsInstaller(filepath.Join(dir, installer)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported release target %q", target)
		}
		info, err := os.Stat(filepath.Join(dir, installer))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("empty installer: %s", installer)
		}
	}
	return nil
}

func verifySigned(dir, name string, size int64, signature string, key ed25519.PublicKey) error {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if size <= 0 || n != size {
		return fmt.Errorf("%s: archive size mismatch", name)
	}
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || !ed25519.Verify(key, h.Sum(nil), sig) {
		return fmt.Errorf("%s: invalid update signature", name)
	}
	return nil
}

func verifyWindowsInstaller(path string) error {
	exe, err := pe.Open(path)
	if err != nil {
		return err
	}
	defer exe.Close()
	// NSIS uses an x86 GUI bootstrapper even for an x64 application.
	gui := false
	switch h := exe.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		gui = h.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI
	case *pe.OptionalHeader64:
		gui = h.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_GUI
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !gui || !bytes.Contains(data, append([]byte{0xef, 0xbe, 0xad, 0xde}, []byte("NullsoftInst")...)) {
		return fmt.Errorf("%s is not an NSIS GUI installer", path)
	}
	return nil
}
