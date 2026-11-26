package uiapp

import (
	"myguo/internal/native"
	"myguo/internal/playback"
)

// MediaHost connects native navigation to the Go playback controller. It
// carries no document, JavaScript dispatcher or WebView lifecycle.
type MediaHost struct {
	controller nativePlayerController
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
	LikeDanmaku(string, bool)
}

func NewNativeMediaHost(controller *playback.Controller) *MediaHost {
	return &MediaHost{controller: controller}
}
func (h *MediaHost) RecommendChannel(channel string) {
	if h.controller != nil {
		h.controller.ShowFeed(channel)
	}
}

func (h *MediaHost) Stop() {
	if h.controller != nil {
		h.controller.Stop()
	}
}

func (h *MediaHost) Play(id string, num int, position float64, session int64) {
	if h.controller != nil {
		h.controller.Play(id, num, position, session)
	}
}
func (h *MediaHost) Command(p native.MediaCommandPayload) { h.PlayerControl(p.Command, p.Position) }
func (h *MediaHost) PictureInPicture()                    { h.PlayerControl("pip", nil) }
func (h *MediaHost) PlayerControl(command string, value any) {
	if h.controller != nil {
		h.controller.Command(command, value)
	}
}
func (h *MediaHost) Snapshot() playback.State {
	if h.controller != nil {
		return h.controller.Snapshot()
	}
	return playback.State{Preferences: playback.DefaultPreferences()}
}
func (h *MediaHost) Close() {
	if h.controller != nil {
		h.controller.Close()
	}
}
