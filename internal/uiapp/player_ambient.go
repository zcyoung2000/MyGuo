package uiapp

import (
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

const (
	playerAmbientWidth     float32 = 180
	playerAmbientAlpha     float32 = .32
	playerAmbientPeak      float32 = 184
	playerAmbientSmoothing         = float32(.18)
	playerAmbientDeadband          = 3
)

var playerAmbientTransition = ui.Animation{Duration: 1100 * time.Millisecond, Ease: ui.EaseInOut}

func smoothPlayerAmbient(current, sample ui.Color) ui.Color {
	if current.A == 0 {
		return sample
	}
	smooth := func(previous, next uint8) uint8 {
		delta := int(next) - int(previous)
		if delta >= -playerAmbientDeadband && delta <= playerAmbientDeadband {
			return previous
		}
		step := int(float32(delta) * playerAmbientSmoothing)
		if step == 0 {
			if delta < 0 {
				step = -1
			} else {
				step = 1
			}
		}
		return uint8(int(previous) + step)
	}
	return ui.RGB(smooth(current.R, sample.R), smooth(current.G, sample.G), smooth(current.B, sample.B))
}

// Tier-4 drawing, MyGo v0.9.1: Box/NativeView retain layout and input. Their
// backgrounds cannot restrict a wash to the changing video gutters, so the
// painter draws a few native radial gradients, without blur passes or captures.
// AnimateColor owns retargeting; Draw only paints immutable color snapshots.
func restrainedPlayerAmbient(color ui.Color) ui.Color {
	r, g, b := float32(color.R), float32(color.G), float32(color.B)
	luma := .2126*r + .7152*g + .0722*b
	low, peak := min(r, g, b), max(r, g, b)
	chroma := peak - low
	if chroma <= 3 || luma <= 4 {
		return ui.Transparent
	}
	// Edge averages are already muted. Recover their hue before compositing
	// over the gray system material, instead of attenuating it a second time.
	// Bound saturation and brightness so the wider wash never becomes neon;
	// neutral/near-black scenes still fade out rather than being amplified.
	saturation := min(float32(.82), max(float32(.58), chroma/peak))
	channel := func(value float32) uint8 {
		return uint8(playerAmbientPeak * (1 - saturation + saturation*(value-low)/chroma))
	}
	alpha := playerAmbientAlpha * min(float32(1), (chroma-3)/21) * min(float32(1), (luma-4)/28)
	return ui.RGBA(channel(r), channel(g), channel(b), alpha)
}

func (a *App) playerAmbientColors(c *ui.Context, element ui.Element, active bool) (left, top ui.Color) {
	prefs := c.Preferences()
	if !active || !a.ambientActive || prefs.ReduceTransparency || prefs.HighContrast {
		return ui.Transparent, ui.Transparent
	}
	left = element.AnimateColor("ambient-left", restrainedPlayerAmbient(a.ambientLeft), playerAmbientTransition)
	top = element.AnimateColor("ambient-top", restrainedPlayerAmbient(a.ambientTop), playerAmbientTransition)
	return
}

func (a *App) renderPlayerSidebarAmbient(c *ui.Context, sidebar ui.Element) {
	if !c.Vibrancy() || !a.hostedScene() || a.playerFixture {
		return
	}
	left, _ := a.playerAmbientColors(c, sidebar, true)
	if left.A == 0 {
		return
	}
	sidebar.Draw(func(p *ui.Painter, r ui.Rect) {
		width := min(playerAmbientWidth, r.W)
		paintPlayerAmbient(p, ui.Rect{X: r.X + r.W - width, Y: r.Y, W: width, H: r.H}, ui.Point{X: 1, Y: .5}, left)
	})
}

func (a *App) renderPlayerAmbient(c *ui.Context, s playback.State) {
	overlay := ui.Box(c.Key("player-ambient")).Absolute().Fill().PassThrough()
	active := s.Mode != playback.Idle && s.Status.Ready && s.Status.Displayed &&
		!s.Status.PictureInPicture && s.Error == "" && s.Preferences.Fit != "cover"
	left, top := a.playerAmbientColors(c, overlay, active)
	if left.A == 0 && top.A == 0 {
		return
	}
	width, height := float32(s.Status.Width), float32(s.Status.Height)
	overlay.Draw(func(p *ui.Painter, r ui.Rect) {
		for _, region := range playerAmbientGutters(r, width, height) {
			color := left
			if region.horizontal {
				color = top
			}
			paintPlayerAmbient(p, region.rect, region.center, color)
		}
	})
}

type playerAmbientRegion struct {
	rect       ui.Rect
	center     ui.Point
	horizontal bool
}

// Gutters partition only the area OUTSIDE the aspect-fit picture. Cropping,
// overlays, drawer widths and fullscreen never require recoloring video pixels.
func playerAmbientGutters(r ui.Rect, width, height float32) [4]playerAmbientRegion {
	if width <= 0 || height <= 0 || r.W <= 0 || r.H <= 0 {
		return [4]playerAmbientRegion{}
	}
	scale := min(r.W/width, r.H/height)
	w, h := min(r.W, width*scale), min(r.H, height*scale)
	x, y := r.X+(r.W-w)/2, r.Y+(r.H-h)/2
	horizontal := min(playerAmbientWidth, max(float32(0), (r.H-h)/2))
	vertical := min(playerAmbientWidth, max(float32(0), (r.W-w)/2))
	return [4]playerAmbientRegion{
		{rect: ui.Rect{X: x - vertical, Y: r.Y, W: vertical, H: r.H}, center: ui.Point{X: 1, Y: .5}},
		{rect: ui.Rect{X: x + w, Y: r.Y, W: vertical, H: r.H}, center: ui.Point{Y: .5}},
		{rect: ui.Rect{X: x, Y: y - horizontal, W: w, H: horizontal}, center: ui.Point{X: .5, Y: 1}, horizontal: true},
		{rect: ui.Rect{X: x, Y: y + h, W: w, H: horizontal}, center: ui.Point{X: .5}, horizontal: true},
	}
}

func paintPlayerAmbient(p *ui.Painter, r ui.Rect, center ui.Point, color ui.Color) {
	if color.A == 0 || r.W <= 0 || r.H <= 0 {
		return
	}
	// Normalize the native gradient into an ellipse. It fades to transparency;
	// no opaque background or mask covers the underlying system material.
	p.Transform(ui.Scaling(r.W, r.H).Then(ui.Translation(r.X, r.Y)), 0, 0, func() {
		p.FillRadialGradient(ui.Rect{W: 1, H: 1}, ui.RadialGradient{
			Center: center, Radius: 1,
			Stops: []ui.GradientStop{{Color: color}, {Color: color.Alpha(.65), At: .5}, {Color: ui.Transparent, At: 1}},
		}, 0)
	})
}
