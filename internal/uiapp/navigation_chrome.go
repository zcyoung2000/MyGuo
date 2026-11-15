package uiapp

import "github.com/egoist/mygo/ui"

var backChevron = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="m14 5-7 7 7 7" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

// MyGo v0.9.0 BackButton requires a ui.Router. App's history also restores
// playback origins and asynchronous data, so use Button inside Toolbar with
// that existing history rather than introducing a second router.
func navigationBack(c *ui.Context, key string, enabled bool, action func()) {
	ui.Button(c.Key(key), "").Label("返回上一页").Tooltip("返回上一页").
		Disabled(!enabled).Width(30).Height(30).Padding(6).Radius(7).
		OnClick(action).Children(func() {
		ui.Icon(c, backChevron).Size(18, 18)
	})
}

func (a *App) renderPageNavigation(c *ui.Context) {
	canBack := a.navIndex > 0 || a.view == "detail" || a.view == "actor"
	ui.Toolbar(c.Key("page-navigation"), func() {
		navigationBack(c, "page-back", canBack, func() { go a.NavigateHistory(-1) })
	}).Label("页面导航").FillWidth().Height(44).Padding(7, 16).DragWindow()
}
