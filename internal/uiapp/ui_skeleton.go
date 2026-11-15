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

var boneWidths = [...]float32{62, 44, 78, 53, 70, 38, 66, 49, 82, 57, 41, 73}

func boneWidth(index int) float32 { return boneWidths[index%len(boneWidths)] }
