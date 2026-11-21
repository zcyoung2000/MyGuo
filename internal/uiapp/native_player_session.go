package uiapp

import "myguo/internal/playback"

// An explicit feed episode choice starts a normal series session. Keeping the
// recommendation page underneath lets the regular back action restore its
// channel, while series playback gets episode navigation and its own controls.
func (a *App) playFeedEpisode(seriesID string, number int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.media == nil || a.mediaOn || a.view != "recommend" || number < 1 {
		return
	}
	s := a.media.Snapshot()
	if s.Mode != playback.Feed || s.Book.ID == "" || s.Book.ID != seriesID ||
		(s.Book.EpisodeCount > 0 && number > s.Book.EpisodeCount) {
		return
	}
	if len(s.Episodes) > 0 {
		playable := false
		for _, episode := range s.Episodes {
			if episode.Number == number {
				playable = episode.Playable
				break
			}
		}
		if !playable {
			return
		}
	}
	position := float64(-1)
	if s.Playback != nil && s.Playback.EpisodeNum == number && s.Error == "" {
		position = s.Status.Position
	}
	a.playSeriesLocked(seriesID, number, position)
}
