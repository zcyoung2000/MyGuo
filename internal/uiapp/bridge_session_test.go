package uiapp

import (
	"encoding/json"
	"testing"

	"myguo/internal/native"
)

func TestResumeBridgeAcceptsJavaScriptPayloadShape(t *testing.T) {
	const javascript = `{
		"seriesId": "123456",
		"episodeNum": 8,
		"positionSec": 725.25,
		"session": 7,
		"sequence": 1
	}`
	var payload ResumeProgress
	if err := json.Unmarshal([]byte(javascript), &payload); err != nil {
		t.Fatal(err)
	}

	app := NewTestApp()
	app.view = "detail"
	app.detail = Detail{ID: "123456"}
	app.mediaOn = true
	app.mediaSession = 7
	shell := NativeShell{App: app}
	shell.ResumeProgress(payload)

	if app.resume.ID != "123456" || app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 725.25 {
		t.Fatalf("JavaScript resume payload was not accepted: %+v", app.resume)
	}
}

func TestResumeBridgeRejectsStaleAndReplayedSequences(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = Detail{ID: "123456"}
	app.mediaOn = true
	app.mediaSession = 7
	shell := NativeShell{App: app}
	shell.ResumeProgress(ResumeProgress{SeriesID: "123456", EpisodeNum: 8, Position: 100, Session: 7, Sequence: 2})
	if app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 100 {
		t.Fatalf("newer resume sequence rejected: %+v", app.resume)
	}
	shell.ResumeProgress(ResumeProgress{SeriesID: "123456", EpisodeNum: 9, Position: 200, Session: 7, Sequence: 1})
	if app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 100 {
		t.Fatalf("out-of-order sequence changed resume: %+v", app.resume)
	}
	shell.ResumeProgress(ResumeProgress{SeriesID: "123456", EpisodeNum: 10, Position: 300, Session: 7, Sequence: 2})
	if app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 100 {
		t.Fatalf("stale sequence changed resume: %+v", app.resume)
	}
}

func TestNativeSceneReplacementKeepsNewestSelection(t *testing.T) {
	host := &MediaHost{}
	host.Play("123456", 1, -1, 1)
	host.Stop()
	host.Play("223456", 2, -1, 2)
	if host.desired["event"] != "play" || host.desired["seriesId"] != "223456" || host.desired["session"] != int64(2) {
		t.Fatalf("new native scene lost: %+v", host.desired)
	}
}

func TestNativeCommandsDispatchOnceWithoutDocumentReadiness(t *testing.T) {
	player := newNativeUIPlayer()
	host := &MediaHost{controller: player}
	host.Command(native.MediaCommandPayload{Command: "pause"})
	host.PlayerControl("seek", 12.5)
	if len(player.commands) != 2 || player.commands[0] != "pause" || player.commands[1] != "seek" {
		t.Fatalf("native commands missing or repeated: %v", player.commands)
	}
	for range 1000 {
		host.PlayerControl("pause", nil)
	}
	if len(host.SentControls()) > 128 {
		t.Fatal("diagnostic command history grows without bound")
	}
}
