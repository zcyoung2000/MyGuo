package uiapp

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func updateSettingsFixture() (*App, SoftwareUpdateState, *ui.Tester) {
	a := NewTestApp()
	s := SoftwareUpdateState{Version: "0.1.6", Enabled: true, AutomaticChecks: true}
	a.SetSoftwareUpdateState(s)
	tt := ui.NewTester(func(c *ui.Context) {
		a.mu.Lock()
		defer a.mu.Unlock()
		theme := hongguoTheme(c)
		c.SetTheme(&theme)
		ui.Column(c).Fill().Padding(20).Gap(12).Children(func() { a.renderSoftwareUpdates(c) })
	}, 900, 520)
	return a, s, tt
}

func clickUpdateSwitch(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleSwitch, name)
	if !ok || info.Disabled {
		t.Fatalf("switch %q unavailable: %+v", name, info)
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
}

func updateActionReceived[T comparable](t *testing.T, calls <-chan T, expected T) {
	t.Helper()
	select {
	case value := <-calls:
		if value != expected {
			t.Fatalf("action = %v, want %v", value, expected)
		}
	case <-time.After(time.Second):
		t.Fatal("update action was not delivered")
	}
}

func TestSoftwareUpdateActionsAndExternalPreferenceChanges(t *testing.T) {
	a, s, tt := updateSettingsFixture()
	checks, downloads, manual := make(chan bool, 1), make(chan bool, 1), make(chan bool, 1)
	a.SetSoftwareUpdateActions(SoftwareUpdateActions{
		Check:                 func() { manual <- true },
		SetAutomaticChecks:    func(v bool) { checks <- v },
		SetAutomaticDownloads: func(v bool) { downloads <- v },
	})
	tt.Frame()
	if err := tt.Click("检查更新"); err != nil {
		t.Fatal(err)
	}
	updateActionReceived(t, manual, true)
	clickUpdateSwitch(t, tt, "自动下载并安装更新")
	updateActionReceived(t, downloads, true)
	s.AutomaticDownloads = true
	a.SetSoftwareUpdateState(s) // same callback as the updater's own window
	tt.Frame()
	if info, _ := tt.AccessByRole(ui.RoleSwitch, "自动下载并安装更新"); !info.Checked {
		t.Fatal("external download preference was not reflected")
	}
	clickUpdateSwitch(t, tt, "自动检查更新")
	updateActionReceived(t, checks, false)
	s.AutomaticChecks = false
	a.SetSoftwareUpdateState(s)
	tt.Frame()
	if info, _ := tt.AccessByRole(ui.RoleSwitch, "自动下载并安装更新"); !info.Disabled || !info.Checked {
		t.Fatalf("disabling checks must preserve but disable automatic downloads: %+v", info)
	}
}

func TestSoftwareUpdateDisabledAndReadyToRestart(t *testing.T) {
	a, s, tt := updateSettingsFixture()
	restart := make(chan bool, 1)
	a.SetSoftwareUpdateActions(SoftwareUpdateActions{
		Check: func() {}, Restart: func() { restart <- true },
		SetAutomaticChecks: func(bool) {}, SetAutomaticDownloads: func(bool) {},
	})
	s.Enabled = false
	a.SetSoftwareUpdateState(s)
	tt.Frame()
	for _, name := range []string{"自动检查更新", "自动下载并安装更新"} {
		if info, _ := tt.AccessByRole(ui.RoleSwitch, name); !info.Disabled {
			t.Fatalf("disabled build offered %s", name)
		}
	}
	if !tt.HasText("当前版本或安装位置不支持自动安装更新") {
		t.Fatal("missing visible explanation for unavailable updater")
	}
	s.Enabled, s.Installed, s.AvailableVersion = true, true, "0.1.7"
	s.LastCheck = time.Date(2026, 11, 25, 14, 0, 0, 0, time.Local)
	a.SetSoftwareUpdateState(s)
	tt.Frame()
	for _, text := range []string{"当前版本 0.1.6", "更新已准备好", "0.1.7", "2026-11-25 14:00"} {
		if !tt.HasText(text) {
			t.Fatalf("missing update status %q: %v", text, tt.Texts())
		}
	}
	if err := tt.Click("重启应用"); err != nil {
		t.Fatal(err)
	}
	updateActionReceived(t, restart, true)
}

func TestSoftwareUpdatesInNativeSettings(t *testing.T) {
	a := NewTestApp()
	a.view = "settings"
	a.SetSoftwareUpdateState(SoftwareUpdateState{Version: "0.1.6", Enabled: true, AutomaticChecks: true})
	a.SetSoftwareUpdateActions(SoftwareUpdateActions{Check: func() {}, SetAutomaticChecks: func(bool) {}, SetAutomaticDownloads: func(bool) {}})
	tt := ui.NewTester(a.Render, 1200, 820)
	tt.Frame()
	for _, text := range []string{"软件更新", "当前版本 0.1.6", "检查更新", "自动检查更新", "自动下载并安装更新"} {
		if !tt.HasText(text) {
			t.Fatalf("settings missing %q", text)
		}
	}
	if tt.WebViewAt(700, 400) != nil {
		t.Fatal("update settings created a webview")
	}
	if dir := os.Getenv("MYGUO_UPDATE_SHOTS"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, "settings-updates.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}
