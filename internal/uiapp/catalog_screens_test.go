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

// Use the captured public taxonomy and optional local covers with the actual
// page renderer. Export is opt-in; responsive/accessibility checks always run.
func TestCatalogGenreLayouts(t *testing.T) {
	panels := officialPanels(t)
	var covers []ui.ImageSource
	if root := os.Getenv("MYGUO_POLISH_COVERS"); root != "" {
		files, _ := os.ReadDir(root)
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), ".jpg") && !strings.HasSuffix(file.Name(), ".png") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(root, file.Name()))
			if err != nil {
				continue
			}
			bitmap, err := ui.DecodeImage(raw)
			if err == nil {
				covers = append(covers, bitmap)
			}
			if len(covers) >= 8 {
				break
			}
		}
	}
	for index, genre := range []string{"short_play", "comic_series", "ai_series"} {
		for _, width := range []int{760, 1000, 1280} {
			t.Run(fmt.Sprintf("%s-%d", genre, width), func(t *testing.T) {
				a := NewTestApp()
				a.catalog.GenreIndex = index
				a.catalog.Filters = convertFilters(panels[genre])
				a.catalog.FiltersLoaded = true
				a.artwork = newArtworkLoader("", nil, nil)
				for i := range 24 {
					url := fmt.Sprintf("fixture:%d", i)
					if len(covers) > 0 {
						a.artwork.entries[url] = artworkEntry{bitmap: covers[i%len(covers)]}
					}
					a.catalog.Items = append(a.catalog.Items, CatalogItem{ID: fmt.Sprint(i + 1), Title: fmt.Sprintf("%s · 页面验收 %02d", a.catalog.Genre[index], i+1), CoverURL: url, EpisodeCount: 40 + i})
				}
				tt := ui.NewTester(a.Render, width, 820)
				tt.Advance(350 * time.Millisecond)
				if !tt.HasText("真人剧") || !tt.HasText("漫剧") || !tt.HasText("AI剧") {
					t.Fatal("genre switcher missing")
				}
				if index == 1 && (!tt.HasText("画风") || !tt.HasText("3D漫") || !tt.HasText("状态")) {
					t.Fatal("comic-specific controls missing")
				}
				if index != 1 && (tt.HasText("画风") || tt.HasText("状态")) {
					t.Fatal("other genre inherited comic controls")
				}
				for _, name := range []string{"discover-page-scroll", "列表筛选"} {
					if box, ok := tt.Find(name); ok && (box.X < 0 || box.X+box.W > float32(width)+1) {
						t.Fatalf("control overflow: %s %+v", name, box)
					}
				}
				if dir := os.Getenv("MYGUO_CATALOG_SHOTS"); dir != "" && width == 1280 {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(dir, genre+".png"))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, tt.Image())
					file.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
