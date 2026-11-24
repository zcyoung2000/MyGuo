package uiapp

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/avkit"
	"myguo/internal/playback"
)

type systemVideoFixture struct {
	avkit.Player
	video *ui.VideoPlayer
}

func (p *systemVideoFixture) VideoPlayer() *ui.VideoPlayer { return p.video }

func TestNativePlayerUsesOfficialVideoAndAppliesFit(t *testing.T) {
	green := color.RGBA{0, 255, 0, 255}
	frame := image.NewPaletted(image.Rect(0, 0, 6, 3), color.Palette{green})
	var data bytes.Buffer
	if err := gif.Encode(&data, frame, nil); err != nil {
		t.Fatal(err)
	}
	decoder, err := ui.NewGIFDecoder(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	video, err := ui.NewVideoPlayer(decoder)
	if err != nil {
		t.Fatal(err)
	}
	defer video.Close()
	engine := &systemVideoFixture{video: video}
	s := playback.State{Engine: engine, Status: avkit.Status{Ready: true, Displayed: true}}
	s.Preferences.Fit = "contain"
	a := NewTestApp()
	tt := ui.NewTester(func(c *ui.Context) { a.renderNativePlayerVideo(c, s) }, 100, 100)
	isGreen := func(x, y int) bool {
		r, g, b, _ := tt.Image().At(x, y).RGBA()
		return g > 60000 && r < 1000 && b < 1000
	}
	if !isGreen(50, 50) || isGreen(50, 5) {
		t.Fatal("official Video did not show a contained frame")
	}
	s.Preferences.Fit = "cover"
	tt.Frame()
	if !isGreen(50, 5) {
		t.Fatal("cover fit was not applied to the system video")
	}
	engine.video = nil // the PiP window owns the video now
	tt.Frame()
	if isGreen(50, 50) {
		t.Fatal("main renderer kept the PiP video attached")
	}
	ui.NewTester(func(c *ui.Context) { a.renderNativePlayerState(c, s) }, 1280, 820)
	if a.playerNativeBackdrop {
		t.Fatal("GPU video controls sampled the desktop instead of the UI surface")
	}
}
