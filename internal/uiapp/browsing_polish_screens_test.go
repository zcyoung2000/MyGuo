package uiapp

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Opt-in review artifacts use the production renderer and local poster files.
// They perform no API calls or account mutations.
func TestExportBrowsingPolish(t *testing.T) {
	dir := os.Getenv("MYGUO_POLISH_SHOTS")
	if dir == "" {
		t.Skip("set MYGUO_POLISH_SHOTS to export browsing previews")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var covers []ui.ImageSource
	files, _ := os.ReadDir(os.Getenv("MYGUO_POLISH_COVERS"))
	for _, file := range files {
		if file.IsDir() || !(strings.HasSuffix(file.Name(), ".jpg") || strings.HasSuffix(file.Name(), ".png")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(os.Getenv("MYGUO_POLISH_COVERS"), file.Name()))
		if err != nil {
			continue
		}
		bitmap, err := ui.DecodeImage(data)
		if err == nil {
			covers = append(covers, bitmap)
		}
		if len(covers) >= 12 {
			break
		}
	}
	makeApp := func(view string) *App {
		a := NewTestApp()
		a.view = view
		a.artwork = newArtworkLoader("", nil, nil)
		var items []CatalogItem
		for i := range 18 {
			source := fmt.Sprintf("local-poster:%d", i)
			if len(covers) > 0 {
				a.artwork.entries[source] = artworkEntry{bitmap: covers[i%len(covers)]}
			}
			items = append(items, CatalogItem{ID: fmt.Sprint(i + 1), Title: fmt.Sprintf("界面验收样本 %02d", i+1), CoverURL: source, EpisodeCount: 40 + i*7, Progress: float64(i * 5)})
		}
		a.catalog.Items = items
		for i, label := range []string{"主题", "设定", "背景", "推荐", "受众", "时间"} {
			labels := []string{"全部", "动作", "冒险", "魔幻", "爱情", "家庭", "家族", "萌宝", "悬疑", "古装", "奇幻", "成长", "都市", "喜剧", "剧情", "逆袭", "惊悚"}
			if i >= 3 {
				labels = labels[:4]
			}
			row := FilterRow{Title: label, Key: label}
			for _, option := range labels {
				row.Options = append(row.Options, FilterOption{Label: option, Value: option})
			}
			a.catalog.Filters = append(a.catalog.Filters, row)
		}
		a.library.Favorites = Library{Items: items, Loaded: true}
		a.library.Likes = Library{Items: items[:10], Loaded: true}
		a.library.History = Library{Items: items, Loaded: true}
		a.search.Items = items
		return a
	}
	save := func(tt *ui.Tester, name string) {
		file, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, tt.Image()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, view := range []string{"discover", "favorites", "history", "search", "settings"} {
		a := makeApp(view)
		tt := ui.NewTester(a.Render, 1280, 820)
		tt.SetDark(true)
		tt.Frame()
		tt.Advance(400 * time.Millisecond)
		save(tt, view)
		if view == "discover" {
			tt.Click("展开设定筛选")
			for i := range 16 {
				tt.Advance(16 * time.Millisecond)
				save(tt, fmt.Sprintf("filter-%02d", i))
			}
		}
	}
}
