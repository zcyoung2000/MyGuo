package uiapp

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// SoftwareUpdateState is refreshed by the native updater, including changes
// made in its own window. The updater owns persistence, downloads and signing.
type SoftwareUpdateState struct {
	Version            string
	Enabled            bool
	AutomaticChecks    bool
	AutomaticDownloads bool
	LastCheck          time.Time
	AvailableVersion   string
	Installed          bool
}

type SoftwareUpdateActions struct {
	Check                 func()
	SetAutomaticChecks    func(bool)
	SetAutomaticDownloads func(bool)
	Restart               func()
}

func (a *App) SetSoftwareUpdateState(state SoftwareUpdateState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.softwareUpdate = state
	a.invalidate()
}

func (a *App) SetSoftwareUpdateActions(actions SoftwareUpdateActions) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.softwareUpdateActions = actions
	a.invalidate()
}

func (a *App) renderSoftwareUpdates(c *ui.Context) {
	s, actions := a.softwareUpdate, a.softwareUpdateActions
	if actions.Check == nil {
		return
	}
	ui.Text(c, "软件更新").FontSize(12).Bold().TextColor(c.Theme().TextMuted)
	a.settingsCard(c, "settings-updates", func() {
		description := "从 GitHub 获取新版本"
		if !s.LastCheck.IsZero() {
			description = "上次检查：" + s.LastCheck.Local().Format("2006-01-02 15:04")
		}
		a.settingsItemRow(c, "update-version", "MyGuo", description, "当前版本 "+s.Version, "检查更新", "subtle", actions.Check)
		if !s.Enabled {
			ui.Text(c, "当前版本或安装位置不支持自动安装更新，请将正式版安装到可写目录后重试。").TextColor(c.Theme().TextMuted)
		}
		a.softwareUpdateSwitch(c, "update-checks", "自动检查更新", "启动后检查新版本，运行期间每天检查一次", s.AutomaticChecks, s.Enabled, actions.SetAutomaticChecks)
		a.softwareUpdateSwitch(c, "update-downloads", "自动下载并安装更新", "开启自动检查后可用；更新在下次启动时生效，不中断播放", s.AutomaticDownloads, s.Enabled && s.AutomaticChecks, actions.SetAutomaticDownloads)
		if s.Installed {
			a.settingsItemRow(c, "update-installed", "更新已准备好", "下次启动时生效，也可以现在重启", s.AvailableVersion, "重启应用", "accent", actions.Restart)
		} else if s.AvailableVersion != "" {
			ui.Textf(c, "发现新版本 %s，点击“检查更新”查看详情", s.AvailableVersion).TextColor(c.Theme().Accent)
		}
	})
}

// MyGo v0.9.1: Switch carries immediate on/off preferences, ButtonBase in
// settingsItemRow carries actions; Row/Column only arrange their descriptions.
func (a *App) softwareUpdateSwitch(c *ui.Context, key, title, description string, value, enabled bool, change func(bool)) {
	ui.Row(c.Key(key)).Gap(c.Theme().Space(4)).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(c.Theme().Space(1)).Children(func() {
			ui.Text(c, title).TextColor(c.Theme().Text)
			ui.Text(c, description).FontSize(c.Theme().Rem(.85)).TextColor(c.Theme().TextMuted)
		})
		if ui.Switch(c.Key(key+"-switch"), &value).Label(title).Disabled(!enabled || change == nil).Changed() {
			// Plugin callbacks refresh the snapshot through SetSoftwareUpdateState;
			// don't hold Render's mutex while calling into the plugin.
			go change(value)
		}
	})
}
