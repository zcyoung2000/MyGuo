package uiapp

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

const sidebarBleedWidth float32 = 144

// Tier-4 drawing (MyGo v0.9.1): Split preserves the panes' layout and input;
// glass.Blur blurs underneath a pane but cannot mirror adjacent content into it.
// This effect retains the sharp content and Gaussian-blurs one narrow backdrop
// strip before reflecting it into the sidebar. It never rebuilds the controls.
var sidebarBleed = shader.MustCompile(`package main

var Edge float
var Scale float
var OriginalOrigin vec2

func Fragment(dst vec4, pos vec2) vec4 {
	if pos.x >= Edge {
		return vec4(0)
	}
	q := vec2(Edge + 72 + (Edge-pos.x)*0.6, pos.y)
	q.x = min(q.x, rectSize().x-1)
	color := backdropSample(q)
	rgb := color.rgb / max(color.a, 0.001)
	chroma := max(rgb.r, max(rgb.g, rgb.b)) - min(rgb.r, min(rgb.g, rgb.b))
	alpha := 0.22 * smoothstep(0, Edge, pos.x) * smoothstep(0.04, 0.18, chroma)
	luma := dot(rgb, vec3(0.2126, 0.7152, 0.0722))
	rgb = clamp(vec3(luma) + (rgb-vec3(luma))*1.15, 0, 1)
	// Source-over this correction to put the color UNDER the existing pixels:
	// the result is original + glow*(1-original.a), preserving sharp text.
	original := image0At(OriginalOrigin + pos*Scale)
	return vec4((rgb*(1-original.a) + original.rgb)*alpha, alpha)
}
`)

type sidebarBleedEffect struct{ width float32 }

func (sidebarBleedEffect) EffectMargin() ui.Point { return ui.Point{} }

func (effect sidebarBleedEffect) PaintEffect(p *ui.Painter, r ui.Rect, content *ui.Bitmap, inset ui.Point) {
	scale := p.Scale()
	p.ImageRegion(content, ui.Rect{X: inset.X, Y: inset.Y, W: r.W * scale, H: r.H * scale}, r, ui.ImageOptions{})
	sidebarWidth := effect.width
	prefs := p.Preferences()
	if prefs.ReduceTransparency || prefs.HighContrast || sidebarWidth <= 0 || sidebarWidth >= r.W {
		return
	}
	width := min(sidebarBleedWidth, sidebarWidth)
	// Include the reflected source in the backdrop capture. The shader emits
	// transparent pixels on the content side, leaving the page untouched.
	strip := ui.Rect{X: r.X + sidebarWidth - width, Y: r.Y, W: width + min(float32(224), r.W-sidebarWidth), H: r.H}
	sidebarBleed.Draw(p, strip, shader.Options{
		BackdropBlur: 32,
		Images:       [4]*ui.Bitmap{content},
		Uniforms: map[string]any{
			"Edge":           width,
			"Scale":          scale,
			"OriginalOrigin": [2]float32{inset.X + (strip.X-r.X)*scale, inset.Y},
		},
	})
}
