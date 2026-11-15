package uiapp

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// All application materials live here. MyGo v0.9.0 provides Glass and Blur;
// this small Material composition adds the dark scrim needed by text panels,
// without Liquid Glass's lens or bright rim. Player controls use native clear
// glass; ordinary pages sample their own dark UI surface with the same profile.
const (
	chromeClarity  = .6
	contentBlur    = 6
	contentOpacity = .92
)

func selectionFill() ui.LinearGradient {
	return ui.LinearGradient{From: ui.Hex("#d91b42"), To: ui.Hex("#b81235"), Angle: 135}
}

func chromeMaterial() glass.Glass {
	// Interactive Glass switches to a shader and cannot sample AVKit. Keep
	// the native material; button hover/press feedback is a separate wash.
	return glass.Glass{Native: true, Style: glass.Clear, Clarity: chromeClarity}
}

func artworkMaterial() glass.Glass {
	m := pageChromeMaterial()
	// Artwork is painted inside the UI surface, above the native material.
	// Sample that surface for badges, using the same clear-glass profile.
	m.Tint = ui.RGBA(0, 0, 0, .9)
	return m
}

func pageChromeMaterial() glass.Glass {
	// Ordinary pages are painted in the UI surface. Native glass samples
	// beneath it and can turn silver even over a black page. Sample the actual
	// page instead, with the same clear profile and a restrained dark tint.
	m := chromeMaterial()
	m.Native = false
	m.Tint = ui.RGBA(0, 0, 0, .4)
	return m
}

func chromeSurface(e ui.Element) ui.Element {
	return e.Material(chromeMaterial()).Border(1, ui.RGBA(255, 255, 255, .12))
}

type frostedContentMaterial struct{}

func (frostedContentMaterial) PaintMaterial(p *ui.Painter, r ui.Rect, radii [4]float32) {
	if p.Preferences().ReduceTransparency {
		p.FillRadii(r, radii, ui.RGB(0, 0, 0))
		return
	}
	glass.Blur{Radius: contentBlur}.PaintMaterial(p, r, radii)
	p.FillRadii(r, radii, ui.RGBA(0, 0, 0, contentOpacity))
}

func contentSurface(e ui.Element) ui.Element {
	return e.Material(frostedContentMaterial{}).
		Border(1, ui.RGBA(255, 255, 255, .09)).
		Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, .3))
}

func chromeButton(c *ui.Context, key, label string) ui.Element {
	b := chromeSurface(ui.ButtonBase(c.Key(key)).Label(label).Focusable().MinHeight(30).Padding(6, 10).Radius(15))
	hoverBackground(b, ui.Transparent, ui.RGBA(255, 255, 255, .12))
	b.Children(func() { ui.Text(c, label).FontSize(12).SingleLine().TextColor(ui.RGBA(255, 255, 255, .9)) })
	return b
}

func panelAction(c *ui.Context, key, label string, action func()) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Label(label).Focusable().MinHeight(30).Padding(6, 10).Radius(8).OnClick(action)
	hoverBackground(b, ui.RGBA(255, 255, 255, .06), ui.RGBA(255, 255, 255, .12))
	b.Children(func() { ui.Text(c, label).FontSize(12).SingleLine().TextColor(ui.RGBA(255, 255, 255, .9)) })
	return b
}

func choiceMenu(c *ui.Context, key, label string, build func(*ui.Menu)) {
	// Let AppKit own placement, keyboard focus, checkmarks and dismissal.
	// In particular, its modal tracking consumes the dismissing click instead
	// of letting that click toggle the video underneath the menu.
	chromeButton(c, key, label).Menu(func(m *ui.Menu) { m.MaxWidth(180); build(m) })
}
