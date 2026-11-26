package releaseassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Verify the signed archive itself: a successful build can silently fall back
// to a PNG icon when the runner's actool cannot compile an Icon Composer file.
func verifyMacOSIcons(archive string, config Config) error {
	fallback := config.MacOS.InfoPlist["CFBundleIconFile"]
	if config.Name == "" || fallback == "" || filepath.Base(fallback) != fallback {
		return fmt.Errorf("macOS icon configuration needs a bundle name and fallback icon file")
	}
	prefix := config.Name + ".app/Contents/"
	expected := map[string][]byte{}
	for name, source := range map[string]string{
		"Assets.car": config.MacOS.Icon,
		fallback:     filepath.Join("resources", "darwin", fallback),
	} {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return fmt.Errorf("empty source icon: %s", source)
		}
		expected[prefix+"Resources/"+name] = data
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var plist []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		want, icon := expected[h.Name]
		if !icon && h.Name != prefix+"Info.plist" {
			continue
		}
		limit := int64(len(want) + 1)
		if !icon {
			limit = 1 << 20
		}
		data, err := io.ReadAll(io.LimitReader(tr, limit))
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || (icon && !bytes.Equal(data, want)) {
			return fmt.Errorf("update archive has an unexpected icon resource: %s", h.Name)
		}
		if icon {
			delete(expected, h.Name)
		} else {
			plist = data
		}
	}
	for name := range expected {
		return fmt.Errorf("update archive is missing icon resource: %s", name)
	}
	// MyGo emits XML plists. Read key/value pairs rather than comparing the
	// serialization, since indentation and the order of keys are immaterial.
	decoder := xml.NewDecoder(bytes.NewReader(plist))
	icons := map[string]string{}
	key := ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid bundle Info.plist: %w", err)
		}
		if start, ok := token.(xml.StartElement); ok {
			switch start.Name.Local {
			case "key":
				if err := decoder.DecodeElement(&key, &start); err != nil {
					return err
				}
			case "string":
				var value string
				if err := decoder.DecodeElement(&value, &start); err != nil {
					return err
				}
				icons[key] = value
				key = ""
			}
		}
	}
	if icons["CFBundleIconName"] != "AppIcon" || icons["CFBundleIconFile"] != fallback {
		return fmt.Errorf("bundle Info.plist does not reference AppIcon and %s", fallback)
	}
	return nil
}
