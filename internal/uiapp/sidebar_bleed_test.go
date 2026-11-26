package uiapp

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Loaded artwork changes the adjacent sidebar in the same rendered frame.
func TestSidebarBleedFollowsCatalogArtwork(t *testing.T) {
	app := NewTestApp()
	app.view = "new"
	app.artwork = newArtworkLoader("", nil, nil)
	setCover := func(c color.RGBA) {
		img := image.NewRGBA(image.Rect(0, 0, 24, 32))
		draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
		app.artwork.entries["cover"] = artworkEntry{bitmap: ui.NewBitmap(img)}
	}
	setCover(color.RGBA{230, 50, 30, 255})
	for i := range 12 {
		app.releases.Items = append(app.releases.Items, CatalogItem{ID: fmt.Sprint(i), Title: fmt.Sprintf("封面 %d", i), CoverURL: "cover"})
	}
	tt := ui.NewTesterWith(app.Render, 1000, 640, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tt.SetVibrancy(true)
	tt.Frame()
	red := tt.Image().RGBAAt(int(defaultSidebarWidth)-8, 250)
	if red.R <= red.B+10 || red.A == 0 || red.A > 65 {
		t.Fatalf("red artwork does not leave a restrained red tint: %+v", red)
	}
	setCover(color.RGBA{30, 70, 230, 255})
	tt.Frame()
	blue := tt.Image().RGBAAt(int(defaultSidebarWidth)-8, 250)
	if blue.B <= blue.R+10 {
		t.Fatalf("new artwork left stale red in the sidebar: %+v", blue)
	}
	app.SetSidebarWidth(420)
	tt.SetScale(2)
	tt.Frame()
	if edge := tt.Image().RGBAAt(2*(420-8), 2*250); edge.B <= edge.R+10 {
		t.Fatalf("reflection lost the resized sidebar edge at Retina scale: %+v", edge)
	}
}

// The reflected strip follows scrolling, fades left, and leaves navigation sharp and clickable.
func TestSidebarBleedScrollAndNavigation(t *testing.T) {
	const width float32 = 210
	var clicked bool
	var scroll ui.ScrollState
	tt := ui.NewTesterWith(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c).Fill().Effect(sidebarBleedEffect{width: width}).Children(func() {
			ui.Row(c).Fill().Children(func() {
				ui.Box(c).Width(width).FillHeight().Children(func() {
					ui.ButtonBase(c).Label("导航").Absolute().Top(20).Right(8).Size(40, 24).
						Background(ui.RGB(255, 255, 255)).OnClick(func() { clicked = true })
				})
				ui.Scroll(c).Grow(1).FillHeight().TrackScroll(&scroll).Children(func() {
					ui.Box(c).FillWidth().Height(180).Background(ui.RGB(230, 40, 20))
					ui.Box(c).FillWidth().Height(180).Background(ui.RGB(20, 40, 230))
				})
			})
		})
	}, 530, 180, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	img := tt.Image()
	near, far := img.RGBAAt(202, 90), img.RGBAAt(20, 90)
	if near.R <= near.B+10 || far.A != 0 {
		t.Fatalf("reflection must fade into a clear sidebar: near=%+v far=%+v", near, far)
	}
	if nav := img.RGBAAt(180, 30); nav != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("reflection repainted opaque navigation: %+v", nav)
	}
	if content := img.RGBAAt(300, 90); content != (color.RGBA{230, 40, 20, 255}) {
		t.Fatalf("reflection changed page pixels: %+v", content)
	}
	if err := tt.Click("导航"); err != nil || !clicked {
		t.Fatalf("reflection blocked navigation: clicked=%t err=%v", clicked, err)
	}
	tt.Scroll(400, 90, 0, 180)
	if after := tt.Image().RGBAAt(202, 90); after.B <= after.R+10 {
		t.Fatalf("reflection did not follow scrolling: %+v, offset=%+v", after, scroll)
	}
	if tt.FramePending() {
		t.Fatal("a static reflection keeps requesting animation frames")
	}
}

// Fine page details must blur into color instead of leaving repeated sharp ghosts.
func TestSidebarBleedBlursDetails(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: 211, W: r.W - 211, H: r.H}, ui.RGB(230, 40, 20), 0)
			for y := float32(0); y < r.H; y += 8 {
				p.Fill(ui.Rect{X: 211, Y: y, W: r.W - 211, H: 4}, ui.RGB(20, 40, 230), 0)
			}
		}).Effect(sidebarBleedEffect{width: 210})
	}, 530, 180)
	lo, hi := uint8(255), uint8(0)
	for y := 70; y <= 110; y++ {
		pixel := tt.Image().RGBAAt(202, y)
		lo, hi = min(lo, pixel.R), max(hi, pixel.R)
	}
	if hi == 0 || hi-lo > 3 {
		t.Fatalf("fine page stripes remain visible in the tint: red range %d..%d", lo, hi)
	}
}

// Accessibility suppresses the tint, and a neutral page does not add a gray scrim.
func TestSidebarBleedAccessibilityAndNeutralContent(t *testing.T) {
	for _, test := range []struct {
		name  string
		prefs ui.Preferences
		color ui.Color
	}{
		{"reduced-transparency", ui.Preferences{ReduceTransparency: true}, ui.RGB(230, 30, 20)},
		{"high-contrast", ui.Preferences{HighContrast: true}, ui.RGB(230, 30, 20)},
		{"neutral", ui.Preferences{}, ui.Hex("#0e0f14")},
	} {
		t.Run(test.name, func(t *testing.T) {
			tt := ui.NewTesterWith(func(c *ui.Context) {
				c.Root().Background(ui.Transparent)
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: 151, W: r.W - 151, H: r.H}, test.color, 0)
				}).Effect(sidebarBleedEffect{width: 150})
			}, 400, 100, ui.TesterOptions{Preferences: test.prefs})
			if pixel := tt.Image().RGBAAt(140, 50); pixel.A != 0 {
				t.Fatalf("unwanted sidebar tint: %+v", pixel)
			}
		})
	}
}

// Export an opt-in visual fixture over a simulated system material, not a live-window capture.
func TestExportSidebarBleed(t *testing.T) {
	root := os.Getenv("MYGUO_SIDEBAR_BLEED_QA")
	if root == "" {
		t.Skip("set MYGUO_SIDEBAR_BLEED_QA to a preview data root containing cached posters")
	}
	files, err := filepath.Glob(filepath.Join(root, "data", "Caches", "myguo", "images", "*.jpg"))
	if err != nil || len(files) == 0 {
		t.Fatalf("cached posters unavailable: %v", err)
	}
	app := NewTestApp()
	app.view = "new"
	app.artwork = newArtworkLoader("", nil, nil)
	for i, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		bitmap, err := ui.DecodeImage(data)
		if err != nil {
			t.Fatal(err)
		}
		app.artwork.entries[file] = artworkEntry{bitmap: bitmap}
		app.releases.Items = append(app.releases.Items, CatalogItem{ID: fmt.Sprint(i), Title: fmt.Sprintf("侧栏晕染预览 %d", i+1), CoverURL: file})
	}
	tt := ui.NewTesterWith(app.Render, 1280, 820, ui.TesterOptions{Dark: true, Preferences: ui.Preferences{ReduceMotion: true}})
	tt.SetVibrancy(true)
	tt.Frame()
	img := image.NewRGBA(tt.Image().Bounds())
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{76, 81, 81, 255}), image.Point{}, draw.Src)
	draw.Draw(img, img.Bounds(), tt.Image(), image.Point{}, draw.Over)
	f, err := os.Create(filepath.Join(root, "headless-sidebar-bleed.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
