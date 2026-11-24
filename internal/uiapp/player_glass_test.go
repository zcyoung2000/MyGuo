package uiapp

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"myguo/internal/playback"
	"os"
	"path/filepath"
	"testing"
)

func TestPlayerGlassWaitsForNativeBackdropAndKeepsItsTint(t *testing.T) {
	a := NewTestApp()
	poster := a.playerChromeMaterial()
	if poster.Native || poster.Tint.A == 0 {
		t.Fatal("unbacked native glass used over UI poster")
	}
	a.playerNativeBackdrop = true
	video := a.playerChromeMaterial()
	if !video.Native || poster.Tint != video.Tint || poster.Style != video.Style {
		t.Fatal("video promotion changed glass tone or disabled native material")
	}
	for _, native := range []bool{false, true} {
		a.playerNativeBackdrop = native
		tt := ui.NewTester(func(c *ui.Context) {
			theme := hongguoTheme(c)
			c.SetTheme(&theme)
			ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() { a.chromeSurface(ui.Box(c).Size(220, 40).Radius(20)) })
		}, 240, 80)
		tt.Frame()
		value := tt.Image().RGBAAt(110, 20)
		if value.R > 60 || value.G > 60 || value.B > 65 {
			t.Fatalf("glass flashes pale before native frame=%v: %+v", native, value)
		}
	}
}

func TestPlayerFeedbackUsesTranslucentMaterial(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, pressure := range []bool{false, true} {
			for _, kind := range []string{"loading", "paused", "error"} {
				t.Run(fmt.Sprintf("%s/dark=%t/pressure=%t", kind, dark, pressure), func(t *testing.T) {
					a := NewTestApp()
					s := playback.State{Mode: playback.Feed, Loading: true}
					if kind == "paused" {
						s.Paused = true
					}
					if kind == "error" {
						s.Error = "视频暂时无法加载，请稍后重试"
					}
					tt := ui.NewTester(func(c *ui.Context) {
						theme := hongguoTheme(c)
						c.SetTheme(&theme)
						ground := ui.RGB(20, 20, 24)
						if pressure {
							ground = ui.Hex("#ff00ff")
						}
						ui.Box(c).Fill().Background(ground).Children(func() {
							a.renderNativePlayerFeedback(c, s, 640, func(string, any) {})
						})
					}, 640, 240)
					tt.SetDark(dark)
					status, ok := tt.AccessByRole(ui.RoleStatus, "播放状态")
					if !ok {
						t.Fatal("feedback strip is inaccessible")
					}
					pixel := tt.Image().RGBAAt(int(status.Bounds.X+8), int(status.Bounds.Y+8))
					if pressure && (pixel.R < 100 || pixel.B < 100 || pixel.G > 40) {
						t.Fatalf("feedback material blocked the magenta ground: %+v", pixel)
					}
					if dir := os.Getenv("MYGUO_PLAYER_FEEDBACK_SHOTS"); dir != "" {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						file, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-dark-%t-pressure-%t.png", kind, dark, pressure)))
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
}
