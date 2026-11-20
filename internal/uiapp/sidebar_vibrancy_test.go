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
	for _, view := range []string{"discover", "history", "recommend"} {
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
