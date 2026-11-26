package uiapp

import (
	"myguo/internal/avkit"
	"myguo/internal/ipc"
	"myguo/internal/playback"
	"sync"
)

// Uses the production renderer and actions, with only AVKit/network replaced.
// Actual decoded media is exercised separately by cmd/player-probe.
type nativeUIPlayer struct {
	nativePlayerController
	mu       sync.Mutex
	state    playback.State
	commands []string
	values   map[string]any
}

func newNativeUIPlayer() *nativeUIPlayer {
	return &nativeUIPlayer{state: playback.State{Mode: playback.Feed, Book: ipc.SeriesCard{ID: "123456", Title: "原生播放测试", EpisodeCount: 812}, Playback: &ipc.PlaybackDTO{SeriesID: "123456", EpisodeID: "223456", EpisodeNum: 22, Quality: "1080p", Qualities: []ipc.QualityOptionDTO{{ID: "720p", Label: "720p", Available: true}, {ID: "1080p", Label: "1080p", Available: true}}}, Status: avkit.Status{Ready: true, Displayed: true, Playing: true, Width: 360, Height: 640, Position: 48, Duration: 200, Buffered: 80}, Preferences: playback.DefaultPreferences()}}
}

func newTestMediaHost() *MediaHost {
	p := &nativeUIPlayer{state: playback.State{Preferences: playback.DefaultPreferences()}}
	return &MediaHost{controller: p}
}

func (p *nativeUIPlayer) Snapshot() playback.State { p.mu.Lock(); defer p.mu.Unlock(); return p.state }
func (p *nativeUIPlayer) ShowFeed(channel string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Mode = playback.Feed
	p.state.Channel = channel
}
func (p *nativeUIPlayer) Play(id string, number int, position float64, session int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Mode = playback.Series
	p.state.Session = session
	p.state.Book.ID = id
	p.state.Panel = ""
	p.state.Paused = false
	p.state.Preferences.Clear = false
	p.state.Status.Playing = true
	d := ipc.PlaybackDTO{}
	if p.state.Playback != nil {
		d = *p.state.Playback
	}
	d.SeriesID = id
	d.EpisodeNum = number
	d.LastPosition = position
	p.state.Playback = &d
}
func (p *nativeUIPlayer) Stop() { p.mu.Lock(); defer p.mu.Unlock(); p.state.Mode = playback.Idle }
func (p *nativeUIPlayer) Command(command string, value any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append(p.commands, command)
	if p.state.Mode == playback.Series {
		switch command {
		case "activity", "seek", "seekBy":
			p.state.ProgressOnly = p.state.Preferences.Clear
		case "surface", "clear", "next", "prev":
			p.state.ProgressOnly = false
		default:
			p.state.Preferences.Clear = false
			p.state.ProgressOnly = false
		}
	}
	if p.values == nil {
		p.values = map[string]any{}
	}
	p.values[command] = value
	switch command {
	case "seek", "seekBy":
		var position float64
		switch v := value.(type) {
		case float64:
			position = v
		case int:
			position = float64(v)
		}
		if command == "seekBy" {
			position += p.state.Status.Position
		}
		p.state.Status.Position = position
	case "panel":
		panel, _ := value.(string)
		if panel == p.state.Panel {
			panel = ""
		}
		p.state.Panel = panel
	case "pause":
		p.state.Paused = true
		p.state.Status.Playing = false
		p.state.Preferences.Clear = false
	case "play":
		p.state.Paused = false
		p.state.Status.Playing = true
	case "toggle":
		p.state.Paused = !p.state.Paused
		p.state.Status.Playing = !p.state.Paused
		if p.state.Paused {
			p.state.Preferences.Clear = false
		}
	case "surface":
		if p.state.Panel != "" {
			p.state.Panel = ""
			p.state.Preferences.Clear = false
			return
		}
		if p.state.Mode == playback.Feed {
			p.state.Paused = !p.state.Paused && !p.state.Status.Ended
			p.state.Status.Playing = !p.state.Paused
			p.state.Preferences.Clear = false
			return
		}
		p.state.Preferences.Clear = !p.state.Preferences.Clear
	case "clear":
		if clear, ok := value.(bool); ok {
			p.state.Preferences.Clear = clear
		} else {
			p.state.Preferences.Clear = !p.state.Preferences.Clear
		}
	}
}
func (p *nativeUIPlayer) Close()                   {}
func (p *nativeUIPlayer) ToggleFavorite()          {}
func (p *nativeUIPlayer) ToggleLike()              {}
func (p *nativeUIPlayer) MoreComments()            {}
func (p *nativeUIPlayer) LoadReplies(string)       {}
func (p *nativeUIPlayer) LikeDanmaku(string, bool) {}
