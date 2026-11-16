package uiapp

import (
	"github.com/egoist/mygo/ui"
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
