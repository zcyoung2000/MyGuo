package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

func TestTopBackReturnsThroughDetailAndActorHistory(t *testing.T) {
	a := NewTestApp()
	a.OpenView("history")
	a.OpenDetail("123456")
	a.OpenActor("actor-id")
	tt := ui.NewTester(a.Render, 1200, 800)
	for _, view := range []string{"detail", "history"} {
		if err := tt.Click("返回上一页"); err != nil {
			t.Fatal(err)
		}
		waitFor(time.Second, func() bool { return a.View() == view })
		if a.View() != view {
			t.Fatalf("back did not restore %s", view)
		}
		tt.Frame()
	}
}

func TestDetailTopBackIsFixedDuringLoadingErrorAndScrolling(t *testing.T) {
	for _, state := range []string{"loading", "error", "ready"} {
		t.Run(state, func(t *testing.T) {
			a := NewTestApp()
			a.OpenDetail("123456")
			switch state {
			case "error":
				a.detail = Detail{Error: "测试加载失败"}
			case "ready":
				a.detail = longDetailEpisodesFixture(812)
			}
			tt := ui.NewTester(a.Render, 1200, 800)
			before, ok := tt.Find("返回上一页")
			if !ok || before.Y > 10 || before.W > 32 || before.H > 32 {
				t.Fatalf("compact top navigation missing: %+v", before)
			}
			tt.Scroll(750, 400, 0, 500)
			tt.Frame()
			after, _ := tt.Find("返回上一页")
			if before != after {
				t.Fatalf("back moved with page content: before=%+v after=%+v", before, after)
			}
			if tt.HasText("← 返回") || tt.HasText("短剧详情") || tt.HasText("演员个人主页") {
				t.Fatal("obsolete page chrome remains")
			}
			if err := tt.Click("返回上一页"); err != nil {
				t.Fatal(err)
			}
			waitFor(time.Second, func() bool { return a.View() == "discover" })
			if a.View() != "discover" {
				t.Fatal("detail loading/error trapped navigation")
			}
		})
	}
}

func TestRootBackIsDisabledAndSeriesUsesSameCompactControl(t *testing.T) {
	a := NewTestApp()
	tt := ui.NewTester(a.Render, 1200, 800)
	back, ok := tt.AccessByRole(ui.RoleButton, "返回上一页")
	if !ok || !back.Disabled {
		t.Fatal("root back must be unavailable without history")
	}
	p := newNativeUIPlayer()
	p.state.Mode = playback.Series
	a.SetMediaHost(&MediaHost{controller: p})
	a.mediaOn = true
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	playerBack, ok := tt.Find("返回上一页")
	if !ok || playerBack.W > 32 || playerBack.Y > 10 {
		t.Fatal("player uses different navigation placement")
	}
	if err := tt.Click("返回上一页"); err != nil {
		t.Fatal(err)
	}
	closed := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.commands) > 0 && p.commands[len(p.commands)-1] == "close"
	}
	waitFor(time.Second, closed)
	if !closed() {
		t.Fatal("player back did not preserve close behavior")
	}
}
