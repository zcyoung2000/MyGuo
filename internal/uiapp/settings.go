package uiapp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

type settingsProvider interface {
	GetProfile(context.Context) (ipc.UserProfile, error)
	GetCacheSize(context.Context) (int64, error)
	BeginQRLogin(context.Context) (*ipc.QRLoginDTO, error)
	PollQRLogin(context.Context, string) (*ipc.QRLoginStatusDTO, error)
	RefreshUserProfile(context.Context) (*ipc.UserProfile, error)
	Logout(context.Context) error
	ClearCache(context.Context) (int64, error)
}

func (a *App) settingsProvider() settingsProvider {
	if a.settingsTestProvider != nil {
		return a.settingsTestProvider
	}
	return a.service
}

func (a *App) LoadSettings() {
	a.mu.Lock()
	if !a.settings.Loading {
		a.mu.Unlock()
		return
	}
	version := uint64(a.generation)
	provider := a.settingsProvider()
	if provider == nil {
		a.finishSettingsLoadLocked(version, ipc.UserProfile{}, 0, errSettingsUnavailable)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	profile, profileErr := provider.GetProfile(ctx)
	cacheSize, cacheErr := provider.GetCacheSize(ctx)
	err := firstError(profileErr, cacheErr)
	a.finishSettingsLoad(version, profile, cacheSize, err)
}

func (a *App) finishSettingsLoad(version uint64, profile ipc.UserProfile, cacheSize int64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	success := a.settings.Success
	a.finishSettingsLoadLocked(version, profile, cacheSize, err)
	a.settings.Success = success
}

func (a *App) finishSettingsLoadLocked(version uint64, profile ipc.UserProfile, cacheSize int64, err error) {
	if version != uint64(a.generation) {
		return
	}
	a.settings.Profile = profile
	a.settings.CacheSize = cacheSize
	a.settings.Loading = false
	a.settings.Error = settingsError(err)
	a.invalidate()
}

func settingsError(err error) string {
	if err == nil {
		return ""
	}
	if err != errSettingsUnavailable {
		log.Printf("[uiapp] settings failed: %v", err)
	}
	return "设置暂时不可用，请稍后重试"
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

var errSettingsUnavailable = errors.New("设置服务不可用")

func formatCacheSize(size int64) string {
	const mb = 1024 * 1024
	return fmt.Sprintf("%.1f MB", float64(size)/float64(mb))
}

func accountBadge(loggedIn bool) string {
	if loggedIn {
		return "已登录账号"
	}
	return "匿名设备访问"
}

func settingsDisplayName(value string) string {
	if strings.TrimSpace(value) == "" {
		return "用户"
	}
	return value
}

func (a *App) RefreshSettingsProfile() {
	a.beginSettingsAction("settings-refresh", func(ctx context.Context, provider settingsProvider) (string, error) {
		if _, err := provider.RefreshUserProfile(ctx); err != nil {
			return "", err
		}
		return "个人资料已刷新", nil
	})
}

func (a *App) LogoutSettings() {
	a.mu.Lock()
	confirm := a.confirm
	loggedIn := a.settings.Profile.LoggedIn
	a.mu.Unlock()
	if loggedIn && confirm != nil && !confirm("确定要退出当前登录账号吗？") {
		return
	}
	a.beginSettingsAction("settings-logout", func(ctx context.Context, provider settingsProvider) (string, error) {
		if err := provider.Logout(ctx); err != nil {
			return "", err
		}
		return "已安全退出登录", nil
	})
}

func (a *App) ClearSettingsCache() {
	a.beginSettingsAction("settings-clear-cache", func(ctx context.Context, provider settingsProvider) (string, error) {
		cleared, err := provider.ClearCache(ctx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已清理 %s 本地媒体缓存", formatCacheSize(cleared)), nil
	})
}

func (a *App) beginSettingsAction(key string, action func(context.Context, settingsProvider) (string, error)) {
	a.mu.Lock()
	if !a.beginSettingsMutationLocked(key) {
		a.mu.Unlock()
		return
	}
	provider := a.settingsProvider()
	version := a.generation
	a.mu.Unlock()
	go a.runSettingsMutation(key, version, func(ctx context.Context) (string, error) {
		return action(ctx, provider)
	})
}

func (a *App) beginSettingsMutationLocked(key string) bool {
	if a.settingsMutations == nil {
		a.settingsMutations = map[string]bool{}
	}
	if a.settingsMutations[key] {
		return false
	}
	a.settingsMutations[key] = true
	a.invalidate()
	return true
}

func (a *App) runSettingsMutation(key string, version uint64, action func(context.Context) (string, error)) {
	a.mu.Lock()
	provider := a.settingsProvider()
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	message, err := action(ctx)
	a.mu.Lock()
	delete(a.settingsMutations, key)
	if version != a.generation {
		a.mu.Unlock()
		return
	}
	a.settings.Success = ""
	a.settings.Error = settingsError(err)
	if err != nil {
		a.invalidate()
		a.mu.Unlock()
		return
	}
	a.settings.Success = message
	a.settings.Loading = true
	a.settings.Error = ""
	a.invalidate()
	if provider == nil {
		a.settings.Loading = false
		a.settings.Error = settingsError(errSettingsUnavailable)
		a.invalidate()
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	profile, profileErr := provider.GetProfile(ctx)
	cacheSize, cacheErr := provider.GetCacheSize(ctx)
	err = firstError(profileErr, cacheErr)
	a.finishSettingsLoad(version, profile, cacheSize, err)
}

func (a *App) settingsButton(c *ui.Context, key, label, variant string, action func()) {
	row := ui.ButtonBase(c.Key(key)).Height(32).Padding(0, 14).Radius(8).
		AlignItems(ui.Center).Justify(ui.Center).
		Role(ui.RoleButton).Label(label).Focusable()
	switch variant {
	case "primary":
		row.LinearGradient(ui.LinearGradient{From: ui.Hex("#d91b42"), To: ui.Hex("#b81235"), Angle: 135}).
			Border(1, ui.RGBA(255, 255, 255, 0.18))
		hoverBackground(row, ui.RGB(209, 27, 66), ui.RGB(229, 36, 75))
	case "danger":
		row.Background(ui.RGBA(255, 77, 95, 0.14)).Border(1, ui.RGBA(255, 77, 95, 0.30))
		hoverBackground(row, ui.RGBA(255, 77, 95, 0.14), ui.RGBA(255, 77, 95, 0.24))
	default:
		row.Background(ui.RGBA(255, 255, 255, 0.06)).Border(1, ui.RGBA(255, 255, 255, 0.09))
		hoverBackground(row, ui.RGBA(255, 255, 255, 0.06), ui.RGBA(255, 255, 255, 0.12))
	}
	if action != nil {
		row.OnClick(func() { go action() })
	}
	row.Children(func() {
		text := ui.Text(c, label).FontSize(12)
		switch variant {
		case "primary":
			text.Bold().TextColor(ui.Hex("#ffffff"))
		case "danger":
			text.TextColor(ui.Hex("#ff8f98"))
		default:
			text.TextColor(ui.RGBA(255, 255, 255, 0.82))
		}
	})
}

func (a *App) settingsCard(c *ui.Context, key string, children func()) {
	ui.Column(c.Key(key)).Grow(1).Gap(16).Padding(20, 22).Radius(12).Clip().Transition(sectionTransition).
		Background(ui.RGBA(20, 21, 28, 0.85)).
		Border(1, ui.RGBA(255, 255, 255, 0.08)).
		Children(children)
}

func (a *App) settingsItemRow(c *ui.Context, key, title, description, value, actionLabel, variant string, action func()) {
	ui.Row(c.Key(key)).Gap(18).AlignItems(ui.Center).Children(func() {
		info := ui.Column(c.Key(key + "-info")).Grow(1).Gap(4).MinWidth(0)
		info.Children(func() {
			ui.Text(c, title).FontSize(14).Bold().TextColor(ui.Hex("#ffffff")).SingleLine()
			if description != "" {
				ui.Text(c, description).FontSize(12).TextColor(c.Theme().TextMuted)
			}
		})
		ui.Row(c.Key(key + "-right")).Gap(12).AlignItems(ui.Center).Justify(ui.End).MinWidth(190).Children(func() {
			if value != "" {
				ui.Text(c, value).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.78)).SingleLine()
			}
			if actionLabel != "" {
				a.settingsButton(c, key+"-action", actionLabel, variant, action)
			}
		})
	})
}

// The profile and its actions stack when the sidebar leaves little content
// width. Badge supplies an unbroken status label; Toolbar owns action overflow.
func (a *App) renderSettingsProfile(c *ui.Context, state Settings) {
	windowWidth, _ := c.Size()
	sidebarWidth := a.sidebarWidth
	if sidebarWidth == 0 {
		sidebarWidth = defaultSidebarWidth
	}
	narrow := windowWidth-sidebarWidth < 700
	var layout ui.Element
	if narrow {
		layout = ui.Column(c.Key("settings-profile-stacked")).Gap(14).FillWidth()
	} else {
		layout = ui.Row(c.Key("settings-profile")).Gap(18).AlignItems(ui.Center)
	}
	layout.Children(func() {
		ui.Row(c.Key("settings-profile-identity")).Grow(1).MinWidth(0).Gap(18).AlignItems(ui.Center).Children(func() {
			ui.Box(c.Key("settings-avatar-host")).Size(58, 58).Shrink(0).Radius(29).
				Background(ui.Hex("#202230")).Border(2, ui.RGBA(255, 255, 255, 0.16)).Clip().Children(func() {
				if state.Profile.Avatar != "" {
					a.image(c, state.Profile.Avatar, 54, 54)
				}
			})
			ui.Column(c.Key("settings-profile-info")).Grow(1).Gap(6).MinWidth(0).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).MinWidth(0).Children(func() {
					nickname := settingsDisplayName(state.Profile.Nickname)
					if !state.Profile.LoggedIn && (state.Profile.Nickname == "" || state.Profile.Nickname == "myguo用户") {
						nickname = "MyGuo 用户"
					}
					ui.Text(c, nickname).FontSize(16).Bold().TextColor(ui.Hex("#ffffff")).SingleLine().MinWidth(0)
					badgeColor, textColor := ui.RGBA(255, 255, 255, 0.08), ui.RGBA(255, 255, 255, 0.70)
					if state.Profile.LoggedIn {
						badgeColor, textColor = ui.RGBA(34, 197, 94, 0.15), ui.Hex("#4ade80")
					}
					ui.Badge(c, accountBadge(state.Profile.LoggedIn)).Padding(2, 8).Background(badgeColor).TextColor(textColor)
				})
			})
		})
		ui.Toolbar(c.Key("settings-profile-actions"), func() {
			a.settingsButton(c, "refresh-profile", "刷新资料", "subtle", a.RefreshSettingsProfile)
			if state.Profile.LoggedIn {
				a.settingsButton(c, "logout", "退出登录", "danger", a.LogoutSettings)
				a.settingsButton(c, "show-login", "切换账号", "primary", func() { a.SetSettingsLoginOpen(true) })
			} else {
				a.settingsButton(c, "show-login", "立即登录", "primary", func() { a.SetSettingsLoginOpen(true) })
			}
		}).Label("账号操作").Gap(10).Padding(0).Shrink(0)
	})
}

func (a *App) renderSettings(c *ui.Context) {
	state := a.settings
	scroll := ui.Scroll(c.Key("settings-scroll")).Scrollbars(false).Grow(1).Gap(22).Padding(0, 0, 18, 0)
	scroll.Children(func() {
		state = a.settings
		a.settingsCard(c, "settings-account", func() {
			a.renderSettingsProfile(c, state)
			if state.LoginOpen {
				a.renderSettingsLogin(c, state)
			}
		})

		ui.Text(c, "存储管理").FontSize(12).Bold().TextColor(c.Theme().TextMuted)
		a.settingsCard(c, "settings-storage", func() {
			a.settingsItemRow(c, "cache-row", "本地媒体缓存", "", formatCacheSize(state.CacheSize), "清理缓存", "subtle", a.ClearSettingsCache)
		})
		a.renderSoftwareUpdates(c)

		if state.Error != "" {
			ui.Text(c, state.Error).FontSize(12).TextColor(ui.Hex("#ff8f98"))
		}
		if state.Success != "" {
			ui.Text(c, state.Success).FontSize(12).TextColor(ui.Hex("#4ade80"))
		}
	})
}
