package playback

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io/fs"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"myguo/internal/sqlitedsn"
)

// MigrateLegacyPreferences reads only the seven player preference keys from
// this app's old WebKit databases. No WebView is created, and no cookies,
// account state or other origins are imported. Existing native settings win.
func MigrateLegacyPreferences(home, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var files []string
	for _, identifier := range []string{"com.myguo.app", "com.hongguo.mygo"} {
		root := filepath.Join(home, "Library", "WebKit", identifier, "WebsiteData", "Default")
		visited := 0
		_ = filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			visited++
			if visited > 4096 {
				return fs.SkipAll
			}
			relative, _ := filepath.Rel(root, candidate)
			if entry.IsDir() {
				if strings.Count(relative, string(filepath.Separator)) > 6 {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.Name() == "localstorage.sqlite3" || strings.HasSuffix(entry.Name(), ".localstorage") {
				files = append(files, candidate)
			}
			return nil
		})
	}
	sort.Slice(files, func(i, j int) bool {
		left, le := os.Stat(files[i])
		right, re := os.Stat(files[j])
		return le == nil && (re != nil || left.ModTime().After(right.ModTime()))
	})
	for _, file := range files {
		values, err := readLegacyPreferenceDB(file)
		if err != nil || len(values) == 0 {
			continue
		}
		return SavePreferences(path, ImportLegacyPreferences(values))
	}
	return nil
}
func readLegacyPreferenceDB(path string) (map[string]string, error) {
	uri := sqlitedsn.FileURL(path)
	query := uri.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(200)")
	query.Add("_pragma", "query_only(1)")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT key,value FROM ItemTable WHERE key IN ('hongguo_danmaku_enabled','hongguo_danmaku_opacity','hongguo_danmaku_font_size','hongguo_danmaku_font_weight','hongguo_danmaku_outline','hongguo_danmaku_rows','hongguo_sidebar_width')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key string
		var raw []byte
		if err = rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		values[key] = legacyText(raw)
	}
	return values, rows.Err()
}
func legacyText(raw []byte) string {
	// WebKit values are UTF-16LE blobs on macOS; older databases may use text.
	if len(raw) >= 2 && len(raw)%2 == 0 {
		utf16Value := true
		for i := 1; i < len(raw); i += 2 {
			if raw[i] != 0 {
				utf16Value = false
				break
			}
		}
		if utf16Value {
			units := make([]uint16, len(raw)/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(raw[i*2:])
			}
			return string(utf16.Decode(units))
		}
	}
	return string(raw)
}
func ImportLegacyPreferences(values map[string]string) Preferences {
	p := DefaultPreferences()
	if v, ok := values["hongguo_danmaku_enabled"]; ok {
		p.Danmaku.Enabled = v != "0"
	}
	if v, ok := values["hongguo_danmaku_outline"]; ok {
		p.Danmaku.Outline = v != "0"
	}
	for _, field := range []struct {
		key    string
		target *float64
	}{{"hongguo_danmaku_opacity", &p.Danmaku.Opacity}, {"hongguo_danmaku_font_size", &p.Danmaku.FontSize}} {
		if value, err := strconv.ParseFloat(values[field.key], 64); err == nil {
			*field.target = value
		}
	}
	if v, err := strconv.Atoi(values["hongguo_danmaku_font_weight"]); err == nil {
		p.Danmaku.FontWeight = v
	}
	if v, err := strconv.Atoi(values["hongguo_danmaku_rows"]); err == nil {
		p.Danmaku.Rows = v
	}
	return p.Normalized()
}
