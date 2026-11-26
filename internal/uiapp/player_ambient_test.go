package uiapp

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
	"myguo/internal/avkit"
	"myguo/internal/playback"
)

func ambientPlayerFixture() (*App, *nativeUIPlayer) {
	app, player := NewTestApp(), newNativeUIPlayer()
	player.state.Status.Width, player.state.Status.Height = 640, 360
	player.state.Status.AmbientLeft = []int{235, 70, 30}
	player.state.Status.AmbientTop = []int{220, 100, 35}
	app.SetMediaHost(&MediaHost{controller: player})
	app.OpenView("recommend")
	app.PlaybackChanged()
	return app, player
}

func TestPlayerAmbientFollowsVideoWithoutRecoloringPicture(t *testing.T) {
	app, player := ambientPlayerFixture()
	pressure := false
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		app.Render(c)
		if pressure {
			c.Root().Background(ui.RGB(255, 0, 255))
		}
	}, 1200, 800)
	tt.SetVibrancy(true)
	tt.Frame()
	sidebar := tt.Image().RGBAAt(int(defaultSidebarWidth)-4, 400)
	if sidebar.R <= sidebar.B+15 || sidebar.A < 65 || sidebar.A > 82 {
		t.Fatalf("warm video did not leave a restrained sidebar wash: %+v", sidebar)
	}
	if pixel := tt.Image().RGBAAt(24, 650); pixel.A != 0 {
		t.Fatalf("ambient color covered the sidebar material: %+v", pixel)
	}
	// A 640:360 picture is centered in the 990:800 stage. Only its top and
	// bottom gutters may change; its center must remain the exact video pixel.
	if pixel := tt.Image().RGBAAt(700, 400); pixel != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("ambient color washed over the actual picture: %+v", pixel)
	}
	if pixel := tt.Image().RGBAAt(700, 115); pixel.R <= pixel.B+10 || pixel.R < 16 || pixel.R > 60 {
		t.Fatalf("top video gutter is missing or too bright: %+v", pixel)
	}
	pressure = true
	tt.Frame()
	if pixel := tt.Image().RGBAAt(24, 650); pixel != (color.RGBA{255, 0, 255, 255}) {
		t.Fatalf("ambient wash obscured the material pressure background: %+v", pixel)
	}
	pressure = false
	player.mu.Lock()
	player.state.Status.AmbientLeft = []int{30, 65, 235}
	player.state.Status.AmbientTop = []int{35, 90, 220}
	player.mu.Unlock()
	app.PlaybackChanged()
	tt.Frame()
	if pixel := tt.Image().RGBAAt(int(defaultSidebarWidth)-4, 400); pixel.B <= pixel.R+2 {
		t.Fatalf("sidebar did not follow the new video sample: %+v", pixel)
	}
	app.SetSidebarWidth(320)
	tt.SetScale(2)
	tt.Frame()
	if pixel := tt.Image().RGBAAt(2*(320-4), 2*400); pixel.B <= pixel.R+2 {
		t.Fatalf("ambient wash lost the resized sidebar edge at Retina scale: %+v", pixel)
	}
	if err := tt.Click("播放画面"); err != nil {
		t.Fatal(err)
	}
	if !player.Snapshot().Paused {
		t.Fatal("ambient overlay blocked the player's surface action")
	}
	tt.SetScale(1)
	app.OpenView("discover")
	tt.Frame()
	if pixel := tt.Image().RGBAAt(320-4, 400); pixel.A != 0 {
		t.Fatalf("video color leaked into a browsing page: %+v", pixel)
	}
}

func TestPlayerAmbientAveragedColorsRemainVisibleOnMaterial(t *testing.T) {
	// Real edge averages mix scenery, faces and shadows. Pure RGB primaries
	// miss regressions where those muted colors disappear into gray material.
	for _, sample := range []struct {
		name  string
		color ui.Color
		hue   func(color.RGBA) int
	}{
		{"grass", ui.RGB(95, 113, 86), func(p color.RGBA) int { return int(p.G) - int(p.B) }},
		{"sky", ui.RGB(82, 103, 125), func(p color.RGBA) int { return int(p.B) - int(p.R) }},
		{"warm", ui.RGB(124, 106, 90), func(p color.RGBA) int { return int(p.R) - int(p.B) }},
	} {
		for _, gray := range []uint8{80, 200} {
			t.Run(fmt.Sprintf("%s/gray=%d", sample.name, gray), func(t *testing.T) {
				background := ui.RGB(gray, gray, gray)
				tt := ui.NewTester(func(c *ui.Context) {
					c.Root().Background(background)
					ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
						paintPlayerAmbient(p, r, ui.Point{X: 1, Y: .5}, restrainedPlayerAmbient(sample.color))
					})
				}, int(playerAmbientWidth), 200)
				if pixel := tt.Image().RGBAAt(int(playerAmbientWidth)-4, 100); sample.hue(pixel) < 28 {
					t.Fatalf("muted sample is not visibly colored on material: %+v", pixel)
				}
				if pixel := tt.Image().RGBAAt(int(playerAmbientWidth)/2, 100); sample.hue(pixel) < 17 {
					t.Fatalf("wash faded before reaching the middle of its feather: %+v", pixel)
				}
				if pixel := tt.Image().RGBAAt(0, 100); pixel != (color.RGBA{gray, gray, gray, 255}) {
					t.Fatalf("wash did not fade completely into material: %+v", pixel)
				}
			})
		}
	}
}

func TestPlayerAmbientHonorsAccessibilityAndCoverFit(t *testing.T) {
	for _, test := range []struct {
		name  string
		prefs ui.Preferences
		cover bool
	}{
		{name: "reduce-transparency", prefs: ui.Preferences{ReduceTransparency: true}},
		{name: "high-contrast", prefs: ui.Preferences{HighContrast: true}},
		{name: "cover", cover: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, player := ambientPlayerFixture()
			if test.cover {
				player.state.Preferences.Fit = "cover"
			}
			tt := ui.NewTesterWith(func(c *ui.Context) {
				c.DisableAnimations()
				app.Render(c)
			}, 1200, 800, ui.TesterOptions{Preferences: test.prefs})
			tt.SetVibrancy(true)
			tt.Frame()
			if pixel := tt.Image().RGBAAt(700, 115); pixel != (color.RGBA{0, 0, 0, 255}) {
				t.Fatalf("%s left a wash on the player: %+v", test.name, pixel)
			}
			if !test.cover {
				before := tt.Image().RGBAAt(int(defaultSidebarWidth)-4, 400)
				app.SetAmbientGlow(false, [3]uint8{}, [3]uint8{})
				tt.Frame()
				if after := tt.Image().RGBAAt(int(defaultSidebarWidth)-4, 400); before != after {
					t.Fatalf("accessibility fallback tinted the sidebar: before=%+v after=%+v", before, after)
				}
			}
		})
	}
}

func TestPlayerAmbientEasesColorAndStopsWhenSettled(t *testing.T) {
	app := NewTestApp()
	app.SetAmbientGlow(true, [3]uint8{235, 50, 20}, [3]uint8{})
	tt := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		box := ui.Box(c.Key("ambient-test")).Fill()
		left, _ := app.playerAmbientColors(c, box, true)
		box.Draw(func(p *ui.Painter, r ui.Rect) {
			paintPlayerAmbient(p, r, ui.Point{X: 1, Y: .5}, left)
		})
	}, 240, 200)
	warm := tt.Image().RGBAAt(235, 100)
	app.SetAmbientGlow(true, [3]uint8{20, 50, 235}, [3]uint8{})
	tt.Frame()
	if pixel := tt.Image().RGBAAt(235, 100); pixel != warm {
		t.Fatalf("a scene cut immediately flashed the target color: before=%+v after=%+v", warm, pixel)
	}
	tt.Advance(playerAmbientTransition.Duration / 2)
	mid := tt.Image().RGBAAt(235, 100)
	if mid == warm {
		t.Fatal("ambient color did not ease toward the new scene")
	}
	tt.Advance(playerAmbientTransition.Duration)
	if cool := tt.Image().RGBAAt(235, 100); cool.B <= cool.R+2 || cool == mid {
		t.Fatalf("ambient transition did not finish at the new scene: %+v", cool)
	}
	if tt.FramePending() {
		t.Fatal("a paused/static frame keeps scheduling ambient redraws")
	}
	app.SetAmbientGlow(false, [3]uint8{}, [3]uint8{})
	tt.Frame()
	if pixel := tt.Image().RGBAAt(235, 100); pixel.A != 0 || tt.FramePending() {
		t.Fatalf("inactive playback left a wash or animation loop: %+v", pixel)
	}
}

func TestPlayerAmbientStatusAndGutterBounds(t *testing.T) {
	app, player := ambientPlayerFixture()
	player.state.Status.AmbientTop = []int{300, -2, 80}
	player.state.Status.AmbientLeft = nil
	app.PlaybackChanged()
	if app.ambientTop != ui.RGB(255, 0, 80) || app.ambientLeft != app.ambientTop {
		t.Fatal("ambient samples must be clamped and support a missing left sample")
	}
	for _, status := range []avkit.Status{
		{Ready: true, Displayed: true, PictureInPicture: true, AmbientTop: []int{200, 60, 20}},
		{Ready: true, AmbientTop: []int{200, 60, 20}},
		{Displayed: true, AmbientTop: []int{200, 60, 20}},
		{Ready: true, Displayed: true},
	} {
		player.state.Status = status
		app.PlaybackChanged()
		if app.ambientActive || app.ambientLeft != ui.Transparent || app.ambientTop != ui.Transparent {
			t.Fatalf("unavailable video retained a palette: %+v", status)
		}
	}
	player.state.Mode = playback.Idle
	player.state.Status = avkit.Status{Ready: true, Displayed: true, AmbientTop: []int{200, 60, 20}}
	app.PlaybackChanged()
	if app.ambientActive {
		t.Fatal("idle playback retained an ambient palette")
	}
	for _, size := range [][2]float32{{360, 640}, {640, 360}, {800, 800}, {0, 0}} {
		r := ui.Rect{X: 210, Y: 20, W: 790, H: 640}
		for _, region := range playerAmbientGutters(r, size[0], size[1]) {
			if region.rect.W == 0 || region.rect.H == 0 {
				continue
			}
			if region.rect.X < r.X || region.rect.Y < r.Y || region.rect.X+region.rect.W > r.X+r.W+.01 || region.rect.Y+region.rect.H > r.Y+r.H+.01 {
				t.Fatalf("ambient gutter escaped its video stage: %+v", region)
			}
			if (!region.horizontal && region.rect.W > playerAmbientWidth) || (region.horizontal && region.rect.H > playerAmbientWidth) {
				t.Fatalf("ambient gutter exceeded its feather width: %+v", region)
			}
		}
	}
	for _, sample := range []ui.Color{ui.RGB(255, 0, 0), ui.RGB(0, 255, 0), ui.RGB(0, 0, 255), ui.RGB(255, 255, 255), ui.RGB(0, 0, 0)} {
		if tint := restrainedPlayerAmbient(sample); tint.A > 82 || float32(max(tint.R, tint.G, tint.B)) > playerAmbientPeak {
			t.Fatalf("ambient color exceeded its brightness/opacity cap: %+v", tint)
		}
	}
	for _, sample := range []ui.Color{ui.RGB(255, 255, 255), ui.RGB(80, 82, 81), ui.RGB(0, 0, 0), ui.RGB(1, 4, 2)} {
		if tint := restrainedPlayerAmbient(sample); tint.A != 0 {
			t.Fatalf("neutral or near-black sample was amplified: %+v -> %+v", sample, tint)
		}
	}
}
