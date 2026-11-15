package playback

import (
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestLegacyPreferencesImportDoesNotLoseDisabledAndZeroValues(t *testing.T) {
	p := ImportLegacyPreferences(map[string]string{"hongguo_danmaku_enabled": "0", "hongguo_danmaku_outline": "0", "hongguo_danmaku_opacity": "0.35", "hongguo_danmaku_font_size": "27", "hongguo_danmaku_font_weight": "900", "hongguo_danmaku_rows": "0"})
	if p.Danmaku.Enabled || p.Danmaku.Outline || p.Danmaku.Opacity != .35 || p.Danmaku.FontSize != 27 || p.Danmaku.FontWeight != 900 || p.Danmaku.Rows != 0 {
		t.Fatalf("legacy preference lost: %+v", p)
	}
	path := filepath.Join(t.TempDir(), "preferences.json")
	if err := SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	if got := LoadPreferences(path); got != p {
		t.Fatalf("native round trip changed preferences: %+v", got)
	}
}
func TestWebKitMigrationReadsUTF16ValuesAndKeepsNativeChoices(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Library", "WebKit", "com.myguo.app", "WebsiteData", "Default", "parent", "origin", "LocalStorage")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "localstorage.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE ItemTable(key TEXT PRIMARY KEY,value BLOB)`)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(s string) []byte {
		units := utf16.Encode([]rune(s))
		raw := make([]byte, len(units)*2)
		for i, u := range units {
			binary.LittleEndian.PutUint16(raw[i*2:], u)
		}
		return raw
	}
	for key, value := range map[string]string{"hongguo_danmaku_enabled": "0", "hongguo_danmaku_font_size": "26", "unrelated": "must never import"} {
		if _, err = db.Exec(`INSERT INTO ItemTable VALUES(?,?)`, key, encode(value)); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	path := filepath.Join(home, "native-player.json")
	if err = MigrateLegacyPreferences(home, path); err != nil {
		t.Fatal(err)
	}
	p := LoadPreferences(path)
	if p.Danmaku.Enabled || p.Danmaku.FontSize != 26 {
		t.Fatalf("WebKit values not migrated: %+v", p)
	}
	p.Danmaku.FontSize = 31
	if err = SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	if err = MigrateLegacyPreferences(home, path); err != nil {
		t.Fatal(err)
	}
	if got := LoadPreferences(path); got.Danmaku.FontSize != 31 {
		t.Fatal("migration overwrote native settings")
	}
}
