package uiapp

import "github.com/egoist/mygo/ui"

// Initial empty surfaces reserve their eventual geometry without a spinner,
// shimmer, timer or loading copy. Refreshes keep their existing content.
type placeholder struct{ base ui.Color }

func pagePlaceholder(c *ui.Context) placeholder                      { return placeholder{base: c.Theme().SurfaceHover} }
func (s placeholder) paint(p *ui.Painter, r ui.Rect, radius float32) { p.Fill(r, s.base, radius) }
func (s placeholder) bone(c *ui.Context, radius float32) ui.Element {
	return ui.Box(c).Shrink(0).Background(s.base).Radius(radius)
}
func (s placeholder) text(c *ui.Context, size, lineHeight float32) ui.Element {
	return ui.Box(c).Shrink(0).Height(size * lineHeight).Children(func() {
		ui.Box(c).FillWidth().Height(size * .78).Radius(size * .25).Background(s.base)
	})
}

// shapedText uses MyGo v0.9.1 Text for the same font metrics and wrapping as
// ready content. Box carries the visible bone; the sizing text is hidden from
// both painting and accessibility. Fixed text bones cannot reserve wrapped rows.
func (s placeholder) shapedText(c *ui.Context, text string, size, width float32, bold bool) ui.Element {
	return ui.Box(c).FillWidth().Children(func() {
		label := ui.Text(c, text).FontSize(size).WidthPercent(100).Invisible()
		if bold {
			label.Bold()
		}
		s.bone(c.Key("bone"), size*.25).Absolute().Left(0).Top(3).WidthPercent(width).Height(size * .78)
	})
}

var boneWidths = [...]float32{62, 44, 78, 53, 70, 38, 66, 49, 82, 57, 41, 73}

func boneWidth(index int) float32 { return boneWidths[index%len(boneWidths)] }
