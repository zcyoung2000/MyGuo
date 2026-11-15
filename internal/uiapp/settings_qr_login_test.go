package uiapp

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

type qrSettingsProvider struct {
	settingsTestProvider
	begin func(context.Context) (*ipc.QRLoginDTO, error)
	poll  func(context.Context, string) (*ipc.QRLoginStatusDTO, error)
}

func (f *qrSettingsProvider) BeginQRLogin(ctx context.Context) (*ipc.QRLoginDTO, error) {
	return f.begin(ctx)
}
func (f *qrSettingsProvider) PollQRLogin(ctx context.Context, id string) (*ipc.QRLoginStatusDTO, error) {
	return f.poll(ctx, id)
}
func qrSettingsFixture(t *testing.T) (*App, *qrSettingsProvider) {
	t.Helper()
	a := NewTestApp()
	a.view = "settings"
	a.settings.Profile = ipc.UserProfile{UserID: "42", LoggedIn: true}
	var pngData bytes.Buffer
	png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	f := &qrSettingsProvider{begin: func(context.Context) (*ipc.QRLoginDTO, error) {
		return &ipc.QRLoginDTO{ID: "local-attempt", Image: pngData.Bytes(), ExpiresAt: time.Now().Add(time.Minute).Unix()}, nil
	}, poll: func(context.Context, string) (*ipc.QRLoginStatusDTO, error) {
		return &ipc.QRLoginStatusDTO{State: "waiting"}, nil
	}}
	a.settingsTestProvider = f
	a.qrPollInterval = 5 * time.Millisecond
	t.Cleanup(a.CloseQRLogin)
	return a, f
}
func waitQRState(t *testing.T, a *App, want string) {
	t.Helper()
	waitFor(time.Second, func() bool { return a.SettingsState().QR.State == want })
	if a.SettingsState().QR.State != want {
		t.Fatalf("QR state=%+v, want %s", a.SettingsState().QR, want)
	}
}
func TestSettingsQRReplacesCredentialFieldsAndKeepsLayoutStable(t *testing.T) {
	a, _ := qrSettingsFixture(t)
	a.SetSettingsLoginOpen(true)
	waitQRState(t, a, "waiting")
	for _, width := range []int{800, 1400} {
		tt := ui.NewTester(a.Render, width, 1000)
		tt.Frame()
		for _, text := range []string{"扫码登录", "刷新二维码", "取消"} {
			if !tt.HasText(text) {
				t.Fatalf("missing %s", text)
			}
		}
		for _, text := range []string{"Cookie", "Token", "验证并登录", "加载中"} {
			if tt.HasText(text) {
				t.Fatalf("manual credentials or loading text remains: %s", text)
			}
		}
		if width == 1400 {
			exportAuditScreenshot(t, tt, "settings-qr-login")
		}
	}
}
func TestSettingsQRScanAndConfirmationUpdateAccount(t *testing.T) {
	a, f := qrSettingsFixture(t)
	var count atomic.Int32
	a.library.History = Library{Loaded: true, Items: []CatalogItem{{ID: "existing"}}}
	f.poll = func(context.Context, string) (*ipc.QRLoginStatusDTO, error) {
		if count.Add(1) == 1 {
			return &ipc.QRLoginStatusDTO{State: "scanned"}, nil
		}
		return &ipc.QRLoginStatusDTO{State: "confirmed", Profile: &ipc.UserProfile{UserID: "42", Nickname: "扫码用户", LoggedIn: true}}, nil
	}
	a.SetSettingsLoginOpen(true)
	waitFor(time.Second, func() bool { return a.SettingsState().Success != "" })
	state := a.SettingsState()
	if state.LoginOpen || state.Profile.Nickname != "扫码用户" || state.Success == "" {
		t.Fatalf("confirmation did not finish: %+v", state)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.library.History.Items) != 1 {
		t.Fatal("same account UI lost history")
	}
}
func TestSettingsCancelAndNavigationIgnoreLateQRResults(t *testing.T) {
	for _, navigate := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "navigate"}[navigate], func(t *testing.T) {
			a, f := qrSettingsFixture(t)
			started := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})
			f.poll = func(context.Context, string) (*ipc.QRLoginStatusDTO, error) {
				close(started)
				<-release
				defer close(finished)
				return &ipc.QRLoginStatusDTO{State: "confirmed", Profile: &ipc.UserProfile{UserID: "99", LoggedIn: true}}, nil
			}
			a.SetSettingsLoginOpen(true)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("poll not started")
			}
			if navigate {
				a.OpenView("discover")
			} else {
				a.SetSettingsLoginOpen(false)
			}
			close(release)
			<-finished
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.settings.LoginOpen || a.settings.Profile.UserID != "42" {
				t.Fatal("late QR replaced canceled account")
			}
		})
	}
}
func TestSettingsQRErrorAndRefreshKeepCurrentAccount(t *testing.T) {
	a, f := qrSettingsFixture(t)
	var calls atomic.Int32
	original := f.begin
	f.begin = func(ctx context.Context) (*ipc.QRLoginDTO, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("二维码已过期，请刷新二维码")
		}
		return original(ctx)
	}
	a.SetSettingsLoginOpen(true)
	waitQRState(t, a, "error")
	if a.SettingsState().Profile.UserID != "42" {
		t.Fatal("failed QR changed account")
	}
	a.SetSettingsLoginOpen(true)
	waitQRState(t, a, "waiting")
	if calls.Load() != 2 {
		t.Fatal("refresh did not request new QR")
	}
}
func TestSettingsQRCodeRefreshDiscardsOldImage(t *testing.T) {
	a, f := qrSettingsFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	original := f.begin
	f.begin = func(ctx context.Context) (*ipc.QRLoginDTO, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			code, err := original(ctx)
			code.ID = "old-attempt"
			return code, err
		}
		return original(ctx)
	}
	a.SetSettingsLoginOpen(true)
	<-started
	a.SetSettingsLoginOpen(true)
	waitQRState(t, a, "waiting")
	close(release)
	a.SetSettingsLoginOpen(false)
	if a.SettingsState().QR.Image != nil {
		t.Fatal("QR image survived cancel")
	}
}
