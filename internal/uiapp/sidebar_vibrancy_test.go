package uiapp

import (
	"bytes"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSidebarTintsSystemMaterialAndKeepsContentOpaque(t *testing.T) {
	for _, view := range []string{"recommend", "discover", "rank", "new", "history", "favorites", "reservations", "settings"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%t", view, dark), func(t *testing.T) {
				app := NewTestApp()
				if view == "recommend" {
					app.SetMediaHost(&MediaHost{controller: newNativeUIPlayer()})
				}
				app.OpenView(view)
				pressure := false
				tt := ui.NewTesterWith(func(c *ui.Context) {
					app.Render(c)
					if pressure {
						c.Root().Background(ui.RGB(255, 0, 255))
					}
				}, 1200, 800, ui.TesterOptions{Dark: dark, Preferences: ui.Preferences{ReduceMotion: true}})
				tt.SetVibrancy(true)
				tt.Frame()
				if pixel := tt.Image().RGBAAt(100, 650); pixel.A < 160 || pixel.A > 170 || pixel.R > 20 || pixel.G > 20 || pixel.B > 20 {
					t.Fatalf("sidebar must retain the guide's dark 65%% veil over native frost: %+v", pixel)
				}
				if pixel := tt.Image().RGBAAt(400, 650); pixel.A != 255 {
					t.Fatalf("content pane became transparent: %+v", pixel)
				}
				pressure = true
				tt.Frame()
				if pixel := tt.Image().RGBAAt(100, 650); pixel != (color.RGBA{255, 0, 255, 255}) {
					t.Fatalf("a sidebar layer blocks the material stress background: %+v", pixel)
				}
				if pixel := tt.Image().RGBAAt(400, 650); pixel == (color.RGBA{255, 0, 255, 255}) {
					t.Fatal("material stress background leaked through the content pane")
				}
				if dir := os.Getenv("MYGUO_SIDEBAR_SHOTS"); dir != "" {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(dir, fmt.Sprintf("sidebar-%s-dark-%t.png", view, dark)))
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					if err := png.Encode(file, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestPopulatedGridDoesNotChangeSidebarMaterial(t *testing.T) {
	for _, view := range []string{"new", "search", "history", "favorites", "likes", "reservations"} {
		t.Run(view, func(t *testing.T) {
			app := NewTestApp()
			app.view = view
			tt := ui.NewTesterWith(app.Render, 1200, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
			tt.SetVibrancy(true)
			tt.Frame()
			before := tt.Image().RGBAAt(100, 650)
			app.releases.Loading = true
			app.search.Loading = true
			app.library.History.Loading = true
			app.library.Favorites.Loading = true
			app.library.Likes.Loading = true
			app.library.Reserved.Loading = true
			tt.Frame()
			if pixel := tt.Image().RGBAAt(100, 650); pixel != before {
				t.Fatalf("loading changed sidebar material from %+v to %+v", before, pixel)
			}
			items := make([]CatalogItem, 12)
			for i := range items {
				items[i] = CatalogItem{ID: fmt.Sprint(i), Title: fmt.Sprintf("侧栏回归剧集%d", i)}
			}
			app.releases = ReleaseState{Items: items}
			app.search.Loading = false
			app.search.Items = items
			app.library.History = Library{Items: items, Loaded: true}
			app.library.Favorites = Library{Items: items, Loaded: true}
			app.library.Likes = Library{Items: items, Loaded: true}
			app.library.Reserved = Library{Items: items, Loaded: true}
			app.refreshVisibleLibraryLocked(libraryHistory)
			app.refreshVisibleLibraryLocked(libraryFavorites)
			app.refreshVisibleLibraryLocked(libraryLikes)
			app.refreshVisibleLibraryLocked(libraryReservations)
			for frame := range 3 {
				tt.Frame()
				if _, ok := tt.Find(items[0].Title); !ok {
					t.Fatal("populated poster grid is missing")
				}
				if pixel := tt.Image().RGBAAt(100, 650); pixel != before {
					t.Fatalf("frame %d: populated grid changed sidebar material from %+v to %+v", frame, before, pixel)
				}
			}
			app.view = "settings"
			tt.Frame()
			app.view = view
			tt.Frame()
			if pixel := tt.Image().RGBAAt(100, 650); pixel != before {
				t.Fatalf("returning to a populated grid changed sidebar material: %+v", pixel)
			}
		})
	}
}

func TestSidebarMaterialFollowsTransparencyPreference(t *testing.T) {
	app := NewTestApp()
	tt := ui.NewTesterWith(app.Render, 1200, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	for _, state := range []struct{ material, reduce bool }{{true, false}, {true, true}, {false, false}, {true, false}} {
		tt.SetVibrancy(state.material)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true, ReduceTransparency: state.reduce})
		tt.Frame()
		alpha := tt.Image().RGBAAt(100, 650).A
		if translucent := state.material && !state.reduce; translucent != (alpha > 0 && alpha < 255) {
			t.Fatalf("material=%t reduceTransparency=%t sidebar alpha=%d", state.material, state.reduce, alpha)
		}
	}
}

func TestSidebarVeilKeepsBrightSystemMaterialDark(t *testing.T) {
	// The acrylic guide measures the brightest system material at #898c8d.
	// A transparent root exposes that gray; the Ground veil must darken it.
	background := windowGroundColor(true).Over(ui.Hex("#898c8d"))
	if background.R > 65 || background.G > 65 || background.B > 70 {
		t.Fatalf("system material washes out the dark sidebar: %+v", background)
	}
	if windowGroundColor(false).A != 255 {
		t.Fatal("a window without system material must use a solid Ground fallback")
	}
}

func TestVideoAmbientColoursDoNotTintSidebar(t *testing.T) {
	app := NewTestApp()
	app.SetMediaHost(&MediaHost{controller: newNativeUIPlayer()})
	app.OpenView("recommend")
	tt := ui.NewTesterWith(app.Render, 1200, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tt.SetVibrancy(true)
	tt.Frame()
	image := tt.Image()
	before := append([]byte(nil), image.Pix...)
	app.SetAmbientGlow(true, [3]uint8{255, 0, 0}, [3]uint8{0, 0, 255})
	tt.Frame()
	image = tt.Image()
	for y := 0; y < image.Bounds().Dy(); y++ {
		start := y * image.Stride
		end := start + int(defaultSidebarWidth-1)*4
		if !bytes.Equal(before[start:end], image.Pix[start:end]) {
			t.Fatalf("video colour changed the sidebar at row %d", y)
		}
	}
}
