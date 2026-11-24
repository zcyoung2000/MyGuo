package uiapp

import (
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

// The promotion grace hides the sub-second spinner on Windows. macOS never
// sets LoadingGrace, so its immediate-spinner contract is asserted here too by
// the LoadingGrace=false case (which is the only shape macOS produces).
func TestPlayerLoadingGraceSuppressesSpinner(t *testing.T) {
	for _, tc := range []struct {
		name        string
		grace       bool
		wantSpinner bool
	}{
		{"grace active hides spinner", true, false},
		{"grace expired shows spinner", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewTestApp()
			s := playback.State{Mode: playback.Series, Loading: true, LoadingGrace: tc.grace}
			tt := ui.NewTester(func(c *ui.Context) {
				theme := hongguoTheme(c)
				c.SetTheme(&theme)
				ui.Box(c).Fill().Background(ui.Hex("#141418")).Children(func() {
					a.renderNativePlayerFeedback(c, s, 640, func(string, any) {})
				})
			}, 640, 240)
			tt.Frame()
			_, hasStatus := tt.AccessByRole(ui.RoleStatus, "播放状态")
			if hasStatus != tc.wantSpinner {
				t.Fatalf("grace=%t: feedback status present=%t, want %t", tc.grace, hasStatus, tc.wantSpinner)
			}
		})
	}
}

func TestPlayerSurfaceDescriptionRespectsLoadingGrace(t *testing.T) {
	s := playback.State{Mode: playback.Feed, Loading: true}
	if got := playerSurfaceDescription(s); got != "正在加载，点击继续播放" {
		t.Fatalf("without grace: description=%q", got)
	}
	s.LoadingGrace = true
	if got := playerSurfaceDescription(s); got == "正在加载，点击继续播放" {
		t.Fatalf("with grace: loading description should be suppressed, got %q", got)
	}
}

// The cover poster is the other image that used to flash between episodes.
// Windows promotion grace must suppress it too, while an ordinary cold
// selection keeps the original poster behavior.
func TestPlayerLoadingGraceSuppressesCoverPoster(t *testing.T) {
	a := NewTestApp()
	a.artwork = newArtworkLoader("", nil, nil)
	poster := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := range 20 {
		for x := range 20 {
			poster.SetRGBA(x, y, color.RGBA{R: 255, B: 255, A: 255})
		}
	}
	a.artwork.entries["grace-poster"] = artworkEntry{bitmap: ui.NewBitmap(poster)}

	s := newNativeUIPlayer().state
	s.Mode = playback.Series
	s.Book.CoverURL = "grace-poster"
	s.Status.Ready, s.Status.Displayed = false, false
	s.LoadingGrace = true
	tt := ui.NewTester(func(c *ui.Context) { a.renderNativePlayerVideo(c, s) }, 640, 640)
	tt.Frame()
	if pixel := tt.Image().RGBAAt(320, 320); pixel.R > 200 && pixel.B > 200 {
		t.Fatal("promotion grace painted the cover poster")
	}

	s.LoadingGrace = false
	tt.Frame()
	if pixel := tt.Image().RGBAAt(320, 320); pixel.R < 200 || pixel.B < 200 {
		t.Fatal("non-grace selection lost its initial poster")
	}
}
