package uiapp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

type settingsProvider interface {
	GetProfile(context.Context) (ipc.UserProfile, error)
	GetCacheSize(context.Context) (int64, error)
	GetDeviceInfo(context.Context) (map[string]string, error)
	CheckExistingAccount(context.Context) (map[string]any, error)
	BeginQRLogin(context.Context) (*ipc.QRLoginDTO, error)
	PollQRLogin(context.Context, string) (*ipc.QRLoginStatusDTO, error)
	RefreshUserProfile(context.Context) (*ipc.UserProfile, error)
	Logout(context.Context) error
	ImportHgplayerAccount(context.Context) (*ipc.UserProfile, error)
	ClearCache(context.Context) (int64, error)
	ResetDevice(context.Context) error
	GetSpareDeviceState(context.Context) (*ipc.SpareDeviceStateDTO, error)
	ImportSpareDevice(context.Context, string, string, string) (*ipc.SpareDeviceStateDTO, error)
	DeleteSpareDevice(context.Context, string) (*ipc.SpareDeviceStateDTO, error)
	SetSpareDeviceRegistration(context.Context, bool) (*ipc.SpareDeviceStateDTO, error)
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
		a.finishSettingsLoadLocked(version, ipc.UserProfile{}, 0, nil, nil, nil, errSettingsUnavailable)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	profile, profileErr := provider.GetProfile(ctx)
	cacheSize, cacheErr := provider.GetCacheSize(ctx)
	device, deviceErr := provider.GetDeviceInfo(ctx)
	legacy, legacyErr := provider.CheckExistingAccount(ctx)
	spare, spareErr := provider.GetSpareDeviceState(ctx)
	err := firstError(profileErr, cacheErr, deviceErr, legacyErr, spareErr)
	a.finishSettingsLoad(version, profile, cacheSize, device, legacy, spare, err)
}

func (a *App) finishSettingsLoad(version uint64, profile ipc.UserProfile, cacheSize int64, device map[string]string, legacy map[string]any, spare *ipc.SpareDeviceStateDTO, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	success := a.settings.Success
	a.finishSettingsLoadLocked(version, profile, cacheSize, device, legacy, spare, err)
	a.settings.Success = success
}

func (a *App) finishSettingsLoadLocked(version uint64, profile ipc.UserProfile, cacheSize int64, device map[string]string, legacy map[string]any, spare *ipc.SpareDeviceStateDTO, err error) {
	if version != uint64(a.generation) {
		return
	}
	a.settings.Profile = profile
	a.settings.CacheSize = cacheSize
	a.settings.DeviceID = device["deviceId"]
	a.settings.DeviceVersion = device["version"]
	a.settings.DeviceArch = device["arch"]
	a.settings.HasLegacy = !profile.LoggedIn && legacy != nil && legacy["exists"] == true
	a.settings.LegacyNickname, _ = legacy["nickname"].(string)
	a.settings.LegacyUserID, _ = legacy["userId"].(string)
	a.settings.SpareDevices = spare
	a.settings.Loading = false
	a.settings.Error = settingsError(err)
	a.invalidate()
}

func settingsError(err error) string {
	if err == nil {
		return ""
	}
	return "设置加载失败：" + err.Error()
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
		return "已登录官方账号"
	}
	return "匿名设备访问"
}

func legacyAccountName(state Settings) string {
	if state.LegacyNickname != "" {
		return state.LegacyNickname
	}
	if state.LegacyUserID != "" {
		return state.LegacyUserID
	}
	return "旧账号"
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

func (a *App) ImportSettingsLegacy() {
	a.beginSettingsAction("settings-import", func(ctx context.Context, provider settingsProvider) (string, error) {
		profile, err := provider.ImportHgplayerAccount(ctx)
		if err != nil {
			return "", err
		}
		return "账号导入成功！当前用户：" + settingsDisplayName(profile.Nickname), nil
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

func (a *App) ResetSettingsDevice() {
	a.mu.Lock()
	confirm := a.confirm
	a.mu.Unlock()
	if confirm != nil && !confirm("确认重置匿名设备指纹？") {
		return
	}
	a.beginSettingsAction("settings-reset-device", func(ctx context.Context, provider settingsProvider) (string, error) {
		if err := provider.ResetDevice(ctx); err != nil {
			return "", err
		}
		return "已成功重置匿名设备", nil
	})
}

func (a *App) SetSpareDeviceField(field, value string) {
	a.mu.Lock()
	switch field {
	case "did":
		a.settings.SpareDeviceDid = value
	case "iid":
		a.settings.SpareDeviceIid = value
	case "uid":
		a.settings.SpareDeviceUid = value
	}
	a.mu.Unlock()
	a.invalidate()
}

func (a *App) ImportSettingsSpareDevice() {
	a.mu.Lock()
	deviceID := strings.TrimSpace(a.settings.SpareDeviceDid)
	installID := strings.TrimSpace(a.settings.SpareDeviceIid)
	userID := strings.TrimSpace(a.settings.SpareDeviceUid)
	if deviceID == "" || installID == "" {
		a.settings.Error = "请输入备用设备的 did 与 iid"
		a.mu.Unlock()
		a.invalidate()
		return
	}
	key := "settings-import-spare"
	if !a.beginSettingsMutationLocked(key) {
		a.mu.Unlock()
		return
	}
	provider := a.settingsProvider()
	version := a.generation
	a.mu.Unlock()
	go a.runSettingsMutation(key, version, func(ctx context.Context) (string, error) {
		state, err := provider.ImportSpareDevice(ctx, deviceID, installID, userID)
		if err != nil {
			return "", err
		}
		a.mu.Lock()
		a.settings.SpareDeviceDid, a.settings.SpareDeviceIid, a.settings.SpareDeviceUid = "", "", ""
		a.settings.SpareDevices = state
		a.mu.Unlock()
		return "备用设备已导入", nil
	})
}

func (a *App) DeleteSettingsSpareDevice(deviceID string) {
	a.beginSettingsAction("settings-delete-spare", func(ctx context.Context, provider settingsProvider) (string, error) {
		state, err := provider.DeleteSpareDevice(ctx, deviceID)
		if err != nil {
			return "", err
		}
		a.mu.Lock()
		a.settings.SpareDevices = state
		a.mu.Unlock()
		return "备用设备已删除", nil
	})
}

func (a *App) SetSettingsSpareRegistration(enabled bool) {
	a.beginSettingsAction("settings-spare-registration", func(ctx context.Context, provider settingsProvider) (string, error) {
		state, err := provider.SetSpareDeviceRegistration(ctx, enabled)
		if err != nil {
			return "", err
		}
		a.mu.Lock()
		a.settings.SpareDevices = state
		a.mu.Unlock()
		if enabled {
			return "备用设备注册已开启（每日最多 1 次）", nil
		}
		return "备用设备注册已关闭", nil
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
	device, deviceErr := provider.GetDeviceInfo(ctx)
	legacy, legacyErr := provider.CheckExistingAccount(ctx)
	spare, spareErr := provider.GetSpareDeviceState(ctx)
	err = firstError(profileErr, cacheErr, deviceErr, legacyErr, spareErr)
	a.finishSettingsLoad(version, profile, cacheSize, device, legacy, spare, err)
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
	case "accent":
		row.Background(ui.RGBA(254, 44, 85, 0.16)).Border(1, ui.RGBA(254, 44, 85, 0.36))
		hoverBackground(row, ui.RGBA(254, 44, 85, 0.16), ui.RGBA(254, 44, 85, 0.28))
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
		case "accent":
			text.TextColor(ui.Hex("#ff5d7c"))
		default:
			text.TextColor(ui.RGBA(255, 255, 255, 0.82))
		}
	})
}

func (a *App) settingsCard(c *ui.Context, key string, children func()) {
	ui.Column(c.Key(key)).Grow(1).Gap(16).Padding(20, 22).Radius(12).Clip().Transition(sectionTransition).
		Background(ui.RGBA(20, 21, 28, 255)).
		Border(1, ui.RGBA(255, 255, 255, 0.08)).
		Children(children)
}

func (a *App) settingsItemRow(c *ui.Context, key, title, description, value, actionLabel, variant string, action func()) {
	ui.Row(c.Key(key)).Gap(18).AlignItems(ui.Center).Children(func() {
		info := ui.Column(c.Key(key + "-info")).Grow(1).Gap(4).MinWidth(0)
		info.Children(func() {
			ui.Text(c, title).FontSize(14).Bold().TextColor(ui.Hex("#ffffff")).SingleLine()
			ui.Text(c, description).FontSize(12).TextColor(c.Theme().TextMuted)
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

func settingsMeta(label, value string) (string, float32) {
	return label + ":" + value, 76
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
					if !state.Profile.LoggedIn && state.Profile.Nickname == "" {
						nickname = "myguo用户"
					}
					ui.Text(c, nickname).FontSize(16).Bold().TextColor(ui.Hex("#ffffff")).SingleLine().MinWidth(0)
					badgeColor, textColor := ui.RGBA(255, 255, 255, 0.08), ui.RGBA(255, 255, 255, 0.70)
					if state.Profile.LoggedIn {
						badgeColor, textColor = ui.RGBA(34, 197, 94, 0.15), ui.Hex("#4ade80")
					}
					ui.Badge(c, accountBadge(state.Profile.LoggedIn)).Padding(2, 8).Background(badgeColor).TextColor(textColor)
				})
				ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
					ui.Text(c, "账号 ID:").FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().Shrink(0)
					ui.Text(c, settingsAccountID(state)).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.72)).SingleLine().MinWidth(0)
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
			if !state.Profile.LoggedIn && state.HasLegacy {
				ui.Row(c.Key("settings-import-banner")).Gap(16).AlignItems(ui.Center).Padding(12, 16).Radius(10).
					Background(ui.RGBA(254, 44, 85, 0.08)).Border(1, ui.RGBA(254, 44, 85, 0.25)).Children(func() {
					ui.Column(c.Key("legacy-info")).Grow(1).Gap(2).Children(func() {
						ui.Text(c, "检测到本机已有登录凭据").FontSize(13).Bold().TextColor(ui.Hex("#ffffff"))
						ui.Textf(c, "检测到本机旧版账号：%s，支持一键同步", legacyAccountName(state)).FontSize(11).TextColor(c.Theme().TextMuted)
					})
					a.settingsButton(c, "import-legacy", "一键同步登录", "accent", a.ImportSettingsLegacy)
				})
			}
			if state.LoginOpen {
				a.renderSettingsLogin(c, state)
			}
		})

		ui.Text(c, "设备与存储管理").FontSize(12).Bold().TextColor(c.Theme().TextMuted)
		a.settingsCard(c, "settings-storage", func() {
			a.settingsItemRow(c, "cache-row", "本地媒体缓存", "清除本地已解密的视频切片缓存与封面海报格式转换临时文件", formatCacheSize(state.CacheSize), "清理缓存", "subtle", a.ClearSettingsCache)
			ui.Box(c.Key("storage-divider")).Height(1).Grow(1).Background(c.Theme().Border)
			a.settingsItemRow(c, "device-row", "匿名设备指纹", "当匿名模式触发官方频控限制或需要重置个性化推荐流时，重新生成设备指纹", state.DeviceID, "重置设备", "danger", a.ResetSettingsDevice)
		})

		a.renderSpareDevices(c, state)

		if state.Error != "" {
			ui.Text(c, state.Error).FontSize(12).TextColor(ui.Hex("#ff8f98"))
		}
		if state.Success != "" {
			ui.Text(c, state.Success).FontSize(12).TextColor(ui.Hex("#4ade80"))
		}
	})
}

func (a *App) renderSpareDevices(c *ui.Context, state Settings) {
	ui.Text(c, "播放备用设备池").FontSize(12).Bold().TextColor(c.Theme().TextMuted)
	a.settingsCard(c, "settings-spare-devices", func() {
		ui.Column(c.Key("spare-intro")).Gap(4).Children(func() {
			ui.Text(c, "仅在播放受限时借用；片库、历史、收藏和登录态始终使用主设备。").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Text(c, "注册新备用设备默认关闭；开启后每日最多 1 次，间隔至少 24 小时。").FontSize(11).TextColor(c.Theme().TextMuted)
		})
		if state.SpareDevices != nil {
			ui.Row(c.Key("spare-registration")).Gap(12).AlignItems(ui.Center).Children(func() {
				enabled := state.SpareDevices.Settings.RegistrationEnabled
				ui.Text(c, "允许注册新备用设备").Grow(1).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.86))
				ui.Switch(c, &enabled).Label("允许注册新备用设备").OnChange(func() {
					a.SetSettingsSpareRegistration(enabled)
				})
			})
			for _, device := range state.SpareDevices.Devices {
				a.settingsItemRow(c, "spare-"+device.DeviceID, "备用设备 "+device.DeviceID,
					spareDeviceDescription(device), spareDeviceStatus(device), "删除", "danger", func() {
						a.DeleteSettingsSpareDevice(device.DeviceID)
					})
			}
		}
		a.renderSpareImport(c, state)
	})
}

func renderSpareInput(c *ui.Context, a *App, key, label, placeholder, value string, setter func(string)) {
	ui.Column(c.Key(key + "-field")).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.80))
		input := value
		ui.TextInput(c, &input).Height(34).Padding(0, 12).Radius(8).
			Background(ui.RGBA(24, 25, 36, 255)).Border(1, ui.RGBA(255, 255, 255, 0.09)).
			Placeholder(placeholder).Label(label).OnChange(func() { setter(input) })
	})
}

func (a *App) renderSpareImport(c *ui.Context, state Settings) {
	ui.Box(c.Key("spare-import-divider")).Height(1).Grow(1).Background(c.Theme().Border)
	ui.Column(c.Key("spare-import")).Gap(12).Children(func() {
		ui.Text(c, "手动导入备用设备").FontSize(14).Bold().TextColor(ui.Hex("#ffffff"))
		ui.Row(c.Key("spare-import-fields")).Gap(10).Children(func() {
			renderSpareInput(c, a, "spare-did", "did (必填)", "输入 device_id", state.SpareDeviceDid, func(v string) { a.SetSpareDeviceField("did", v) })
			renderSpareInput(c, a, "spare-iid", "iid (必填)", "输入 install_id", state.SpareDeviceIid, func(v string) { a.SetSpareDeviceField("iid", v) })
			renderSpareInput(c, a, "spare-uid", "uid (可选)", "输入 user_id", state.SpareDeviceUid, func(v string) { a.SetSpareDeviceField("uid", v) })
		})
		a.settingsButton(c, "spare-import-action", "导入备用设备", "primary", a.ImportSettingsSpareDevice)
	})
}

func spareDeviceStatus(device ipc.SpareDeviceDTO) string {
	switch device.Status {
	case "borrowed":
		return "借用中"
	case "disabled":
		return "已停用"
	default:
		return "可用"
	}
}

func spareDeviceDescription(device ipc.SpareDeviceDTO) string {
	return fmt.Sprintf("iid：%s · 失败 %d 次 · 状态：%s", device.InstallID, device.FailureCount, spareDeviceStatus(device))
}

func settingsAccountID(state Settings) string {
	if state.Profile.UserID != "" {
		return state.Profile.UserID
	}
	if state.Profile.LoggedIn {
		return "已同步"
	}
	return "anonymous"
}
