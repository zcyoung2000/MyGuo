package uiapp

import (
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

func TestSeekNeverReplacesDisplayedVideoWithPoster(t *testing.T) {
	for _, mode := range []playback.Mode{playback.Feed, playback.Series} {
		t.Run(string(mode), func(t *testing.T) {
			a := NewTestApp()
			a.artwork = newArtworkLoader("", nil, nil)
			poster := image.NewRGBA(image.Rect(0, 0, 20, 20))
			for y := range 20 {
				for x := range 20 {
					poster.SetRGBA(x, y, color.RGBA{R: 255, B: 255, A: 255})
				}
			}
			a.artwork.entries["seek-poster"] = artworkEntry{bitmap: ui.NewBitmap(poster)}
			s := newNativeUIPlayer().state
			s.Mode = mode
			s.Book.CoverURL = "seek-poster"
			s.HasDisplayedFrame = true
			tt := ui.NewTester(func(c *ui.Context) { a.renderNativePlayerVideo(c, s) }, 640, 640)
			for _, paused := range []bool{false, true} {
				s.Paused = paused
				s.Status.Ready, s.Status.Displayed, s.Status.Buffering = false, false, true
				tt.Frame()
				pixel := tt.Image().RGBAAt(320, 320)
				if pixel.R > 200 && pixel.B > 200 {
					t.Fatal("seek painted the cover over the decoded surface")
				}
				if nativePlayerPosterVisible(s) {
					t.Fatal("seek revived initial loading poster")
				}
			}
			s.HasDisplayedFrame = false // new episode/source selection
			tt.Frame()
			pixel := tt.Image().RGBAAt(320, 320)
			if pixel.R < 200 || pixel.B < 200 {
				t.Fatal("new selection lost its initial poster")
			}
			s.HasDisplayedFrame = true
			s.Error = "视频暂时无法播放"
			tt.Frame()
			if !nativePlayerPosterVisible(s) {
				t.Fatal("terminal error suppressed the poster")
			}
		})
	}
}
