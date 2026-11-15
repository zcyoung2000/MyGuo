package uiapp

import (
	"github.com/egoist/mygo"
	"myguo/internal/native"
	"myguo/internal/playback"
	"sync"
)

// MediaHost connects native navigation to the Go playback controller. It
// carries no document, JavaScript dispatcher or WebView lifecycle.
type MediaHost struct {
	mu         sync.Mutex
	controller nativePlayerController
	desired    map[string]any
	sent       []map[string]any
}

type nativePlayerController interface {
	ShowFeed(string)
	Play(string, int, float64, int64)
	Stop()
	Command(string, any)
	Snapshot() playback.State
	Close()
	ToggleFavorite()
	ToggleLike()
	MoreComments()
	LoadReplies(string)
	SendDanmaku(string, string)
	LikeDanmaku(string, bool)
}

func NewNativeMediaHost(controller *playback.Controller) *MediaHost {
	return &MediaHost{controller: controller}
}
func (h *MediaHost) record(payload map[string]any) { h.mu.Lock(); h.desired = payload; h.mu.Unlock() }
func (h *MediaHost) Recommend()                    { h.RecommendChannel("") }
func (h *MediaHost) RecommendChannel(channel string) {
	h.record(map[string]any{"event": "recommend", "channel": channel})
	if h.controller != nil {
		h.controller.ShowFeed(channel)
	}
}
func (h *MediaHost) Settings()        { h.Stop() }
func (h *MediaHost) Detail(id string) { h.Stop() }
func (h *MediaHost) Stop() {
	h.record(map[string]any{"event": "stop"})
	if h.controller != nil {
		h.controller.Stop()
	}
}
func (h *MediaHost) StopNow() { h.Stop() }
func (h *MediaHost) Play(id string, num int, position float64, session int64) {
	var resume any
	if position >= 0 {
		resume = position
	}
	h.record(map[string]any{"event": "play", "seriesId": id, "episodeNum": num, "resumeAt": resume, "session": session})
	if h.controller != nil {
		h.controller.Play(id, num, position, session)
	}
}
func (h *MediaHost) Command(p native.MediaCommandPayload) { h.PlayerControl(p.Command, p.Position) }
func (h *MediaHost) PictureInPicture()                    { h.PlayerControl("pip", nil) }
func (h *MediaHost) PlayerControl(command string, value any) {
	h.mu.Lock()
	h.PlayerControlLocked(command, value)
	h.mu.Unlock()
}
func (h *MediaHost) PlayerControlLocked(command string, value any) {
	h.sent = append(h.sent, map[string]any{"event": "command", "command": command, "value": value})
	if len(h.sent) > 128 {
		h.sent = h.sent[len(h.sent)-128:]
	}
	if h.controller != nil {
		h.controller.Command(command, value)
	}
}
func (h *MediaHost) Snapshot() playback.State {
	if h.controller != nil {
		return h.controller.Snapshot()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s := playback.State{Preferences: playback.DefaultPreferences()}
	if h.desired["event"] == "recommend" {
		s.Mode = playback.Feed
	} else if h.desired["event"] == "play" {
		s.Mode = playback.Series
	}
	return s
}
func (h *MediaHost) Close() {
	if h.controller != nil {
		h.controller.Close()
	}
}
func (h *MediaHost) SentControls() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.sent...)
}

// NativeShell retains compatibility with existing session and navigation callers.
type NativeShell struct{ App *App }

type AmbientGlowPayload struct {
	Active bool     `json:"active"`
	Left   [3]uint8 `json:"left"`
	Top    [3]uint8 `json:"top"`
}

func (s *NativeShell) AmbientGlow(payload AmbientGlowPayload) {
	s.App.SetAmbientGlow(payload.Active, payload.Left, payload.Top)
}

func (s *NativeShell) OpenDetail(id string)                  { s.App.OpenDetail(id) }
func (s *NativeShell) OpenActor(id string)                   { s.App.OpenActor(id) }
func (s *NativeShell) ResumeProgress(payload ResumeProgress) { s.App.UpdateResumeProgress(payload) }
func (s *NativeShell) Closed(session int64)                  { s.App.CloseMediaSession(session) }
func (s *NativeShell) ClosePlayer(session int64)             { s.App.ClosePlayer(session) }
func (s *NativeShell) LibraryChanged()                       { s.App.LibraryChanged() }
func (s *NativeShell) ToggleFullscreen() {
	if w := mygo.FocusedWindow(); w != nil {
		w.ToggleFullScreen()
		return
	}
	if windows := mygo.Windows(); len(windows) > 0 {
		windows[0].ToggleFullScreen()
		return
	}
	native.ToggleWindowFullscreen()
}
