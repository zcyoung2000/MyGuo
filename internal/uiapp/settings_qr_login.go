package uiapp

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"
)

type QRLoginState struct {
	Image ui.ImageSource
	State string
	Error string
}

type favoriteSyncStarter interface {
	StartFavoriteSync()
}

func (a *App) cancelSettingsLoginLocked() {
	a.loginVersion++
	if a.loginCancel != nil {
		a.loginCancel()
		a.loginCancel = nil
	}
	a.settings.LoginOpen = false
	a.settings.QR = QRLoginState{}
}
func (a *App) CloseQRLogin() { a.SetSettingsLoginOpen(false) }
func (a *App) SetSettingsLoginOpen(open bool) {
	a.mu.Lock()
	a.cancelSettingsLoginLocked()
	if !open {
		a.invalidate()
		a.mu.Unlock()
		return
	}
	a.settings.LoginOpen = true
	a.settings.QR.State = "preparing"
	a.settings.Error = ""
	a.settings.Success = ""
	ctx, cancel := context.WithCancel(context.Background())
	a.loginCancel = cancel
	version, provider, interval := a.loginVersion, a.settingsProvider(), a.qrPollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	a.invalidate()
	a.mu.Unlock()
	go a.runQRLogin(ctx, cancel, version, provider, interval)
}
func (a *App) qrLoginCurrentLocked(ctx context.Context, version uint64) bool {
	return ctx.Err() == nil && a.loginVersion == version && a.settings.LoginOpen
}
func (a *App) failQRLogin(ctx context.Context, version uint64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.qrLoginCurrentLocked(ctx, version) {
		return
	}
	a.settings.QR = QRLoginState{State: "error", Error: userFacingError(err)}
	a.loginCancel = nil
	a.invalidate()
}
func (a *App) runQRLogin(ctx context.Context, cancel context.CancelFunc, version uint64, provider settingsProvider, interval time.Duration) {
	defer cancel()
	if provider == nil {
		a.failQRLogin(ctx, version, errSettingsUnavailable)
		return
	}
	code, err := provider.BeginQRLogin(ctx)
	if err != nil {
		a.failQRLogin(ctx, version, err)
		return
	}
	if code == nil || code.ID == "" || code.ExpiresAt <= time.Now().Unix() {
		a.failQRLogin(ctx, version, errors.New("二维码已过期，请刷新二维码"))
		return
	}
	bitmap, err := ui.DecodeBitmap(code.Image)
	if err != nil {
		a.failQRLogin(ctx, version, errors.New("二维码图片无效，请刷新二维码"))
		return
	}
	a.mu.Lock()
	if !a.qrLoginCurrentLocked(ctx, version) {
		a.mu.Unlock()
		return
	}
	a.settings.QR = QRLoginState{State: "waiting", Image: bitmap}
	a.invalidate()
	a.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	expiry := time.NewTimer(time.Until(time.Unix(code.ExpiresAt, 0).Add(10 * time.Second)))
	defer expiry.Stop()
	confirming := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-expiry.C:
			a.failQRLogin(ctx, version, errors.New("二维码已过期，请刷新二维码"))
			return
		case <-ticker.C:
		}
		result, err := provider.PollQRLogin(ctx, code.ID)
		if err != nil {
			a.failQRLogin(ctx, version, err)
			return
		}
		if result == nil {
			a.failQRLogin(ctx, version, errors.New("扫码状态异常，请刷新二维码"))
			return
		}
		a.mu.Lock()
		if !a.qrLoginCurrentLocked(ctx, version) {
			a.mu.Unlock()
			return
		}
		switch result.State {
		case "confirmed":
			if result.Profile == nil || !result.Profile.LoggedIn || result.Profile.UserID == "" {
				a.mu.Unlock()
				a.failQRLogin(ctx, version, errors.New("登录校验未通过，请重新扫码"))
				return
			}
			if a.settings.Profile.UserID != result.Profile.UserID {
				a.library = LibraryView{Grids: a.library.Grids}
				a.resume = CatalogItem{}
			}
			a.settings.Profile = *result.Profile
			a.generation++
			a.settings.Loading = false
			a.settings.LoginOpen = false
			a.settings.QR = QRLoginState{}
			a.settings.Success = "登录成功！欢迎回来，" + settingsDisplayName(result.Profile.Nickname)
			a.settings.Error = ""
			a.loginCancel = nil
			a.invalidateLibraryReadsLocked()
			a.invalidate()
			a.mu.Unlock()
			if starter, ok := provider.(favoriteSyncStarter); ok {
				starter.StartFavoriteSync()
			}
			return
		case "waiting", "scanned", "confirming":
			if result.State == "confirming" && !confirming {
				confirming = true
				expiry.Reset(45 * time.Second)
			}
			a.settings.QR.State = result.State
		default:
			a.mu.Unlock()
			a.failQRLogin(ctx, version, errors.New("扫码会话已结束，请刷新二维码"))
			return
		}
		a.invalidate()
		a.mu.Unlock()
	}
}

// A login QR is an image, not an editable form. Image and ButtonBase provide
// native accessibility; a fixed square keeps layout stable during refresh.
func (a *App) renderSettingsLogin(c *ui.Context, state Settings) {
	ui.Divider(c)
	ui.Column(c.Key("settings-login")).Gap(12).FillWidth().Children(func() {
		ui.Text(c, "扫码登录").FontSize(14).Bold()
		ui.Text(c, "打开手机 App，扫一扫后确认登录").FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Box(c.Key("login-qr-host")).Size(216, 216).Shrink(0).Radius(12).Padding(12).
			Background(ui.Hex("#191b23")).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if state.QR.Image != nil {
				ui.Box(c).Size(192, 192).Padding(8).Background(ui.Hex("#ffffff")).Children(func() { ui.Image(c, state.QR.Image).Size(176, 176).Label("登录二维码") })
			} else if state.QR.Error != "" {
				ui.Text(c, state.QR.Error).FontSize(12).TextColor(c.Theme().TextMuted).MaxLines(4)
			}
		})
		if state.QR.State == "scanned" {
			ui.Text(c, "已扫码，请在手机上确认").FontSize(12).TextColor(ui.Hex("#4ade80"))
		}
		if state.QR.State == "confirming" {
			ui.Text(c, "手机已确认").FontSize(12).TextColor(ui.Hex("#4ade80"))
		}
		ui.Row(c).Gap(10).Children(func() {
			a.settingsButton(c, "refresh-login-qr", "刷新二维码", "subtle", func() { a.SetSettingsLoginOpen(true) })
			a.settingsButton(c, "cancel-login", "取消", "subtle", func() { a.SetSettingsLoginOpen(false) })
		})
	})
}
