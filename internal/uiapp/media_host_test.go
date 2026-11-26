package uiapp

import (
	"testing"

	"myguo/internal/native"
	"myguo/internal/playback"
)

func TestResumeProgressAcceptsCurrentPlaybackSession(t *testing.T) {
	payload := ResumeProgress{SeriesID: "123456", EpisodeNum: 8, Position: 725.25, Session: 7, Sequence: 1}

	app := NewTestApp()
	app.view = "detail"
	app.detail = Detail{ID: "123456"}
	app.mediaOn = true
	app.mediaSession = 7
	app.UpdateResumeProgress(payload)

	if app.resume.ID != "123456" || app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 725.25 {
		t.Fatalf("native resume progress was not accepted: %+v", app.resume)
	}
}

func TestResumeProgressRejectsStaleAndReplayedSequences(t *testing.T) {
	app := NewTestApp()
	app.view = "detail"
	app.detail = Detail{ID: "123456"}
	app.mediaOn = true
	app.mediaSession = 7
	app.UpdateResumeProgress(ResumeProgress{SeriesID: "123456", EpisodeNum: 8, Position: 100, Session: 7, Sequence: 2})
	if app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 100 {
		t.Fatalf("newer resume sequence rejected: %+v", app.resume)
	}
	app.UpdateResumeProgress(ResumeProgress{SeriesID: "123456", EpisodeNum: 9, Position: 200, Session: 7, Sequence: 1})
	if app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 100 {
		t.Fatalf("out-of-order sequence changed resume: %+v", app.resume)
	}
	app.UpdateResumeProgress(ResumeProgress{SeriesID: "123456", EpisodeNum: 10, Position: 300, Session: 7, Sequence: 2})
	if app.resume.ResumeEpisode != 8 || app.resume.ResumePosition != 100 {
		t.Fatalf("stale sequence changed resume: %+v", app.resume)
	}
}

func TestNativeSceneReplacementKeepsNewestSelection(t *testing.T) {
	host := newTestMediaHost()
	host.Play("123456", 1, -1, 1)
	host.Stop()
	host.Play("223456", 2, -1, 2)
	if host.Snapshot().Mode != playback.Series || host.Snapshot().Book.ID != "223456" || host.Snapshot().Session != int64(2) {
		t.Fatalf("new native scene lost: %+v", host.Snapshot())
	}
}

func TestNativeCommandsDispatchOnce(t *testing.T) {
	player := newNativeUIPlayer()
	host := &MediaHost{controller: player}
	host.Command(native.MediaCommandPayload{Command: "pause"})
	host.PlayerControl("seek", 12.5)
	if len(player.commands) != 2 || player.commands[0] != "pause" || player.commands[1] != "seek" {
		t.Fatalf("native commands missing or repeated: %v", player.commands)
	}
}
