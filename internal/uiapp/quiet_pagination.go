package uiapp

import "github.com/egoist/mygo/ui"

const maxEmptyPages = 3

type pageFillVersion struct {
	scope, cursor string
	count         int
}

// Native List.EndReached owns ordinary scrolling. InView fills a short first
// page and watches the actual end of a related-works section (one list row).
// Visibility and scroll bounds describe the previous layout: after an append,
// wait one layout before using them, or a formerly visible tail overfetches.
func (a *App) continuePageInView(c *ui.Context, key string, version pageFillVersion, margin float32, eligible bool, next func()) {
	tail := ui.Spacer(c.Key(key)).Height(1)
	visible := tail.InViewMargin(0, 0, margin, 0)
	if a.pageLayouts == nil {
		a.pageLayouts = make(map[string]pageFillVersion)
	}
	if prior, ok := a.pageLayouts[key]; !ok || prior != version {
		a.pageLayouts[key] = version
		c.Invalidate()
		return
	}
	if visible && eligible {
		go next()
	}
}
