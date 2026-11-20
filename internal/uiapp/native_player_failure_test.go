package uiapp

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

func TestPlaybackErrorsNeverShowFailureCardOrRetryPrompt(t *testing.T) {
	for _, mode := range []playback.Mode{playback.Feed, playback.Series} {
		p := newNativeUIPlayer()
		p.state.Mode = mode
		p.state.Error = "视频暂时无法加载，请稍后重试"
		a := NewTestApp()
		a.SetMediaHost(&MediaHost{controller: p})
		a.OpenView("recommend")
		p.state.Mode = mode
		tt := ui.NewTester(a.Render, 1280, 820)
		tt.Advance(200 * time.Millisecond)
		if tt.HasText(p.state.Error) || tt.HasText("重新加载") {
			t.Fatal("failure prompt still covers playback")
		}
		stage, ok := tt.AccessByRole(ui.RoleButton, "播放画面")
		if !ok {
			t.Fatal("picture remains inaccessible")
		}
		if stage.Description == "请使用重新加载按钮重试" {
			t.Fatal("assistive text points to removed prompt")
		}
		if err := tt.Click("播放"); err != nil {
			t.Fatal(err)
		}
		if len(p.commands) == 0 || p.commands[len(p.commands)-1] != "toggle" {
			t.Fatal("ordinary play control could not request recovery")
		}
		if err := tt.Click("下一集"); err != nil {
			t.Fatal(err)
		}
		if len(p.commands) == 0 || p.commands[len(p.commands)-1] != "nextEpisode" {
			t.Fatal("failure blocked navigation")
		}
	}
}
