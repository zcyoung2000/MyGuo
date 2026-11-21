package store

import "testing"

func TestHistoryDurationBelongsToWatchedEpisode(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	item := HistoryItem{SeriesID: "710000000000001", Title: "Short series", EpisodeNum: 1, ContentType: 1, TotalEpisodes: 2, DurationSec: 100, PositionSec: 96}
	if err := s.SaveHistory(item); err != nil {
		t.Fatal(err)
	}
	item.EpisodeNum = 2
	item.DurationSec = 0
	item.PositionSec = 96
	if err := s.SaveHistoryBatch([]HistoryItem{item}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetHistory()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].DurationSec != 0 {
		t.Fatalf("borrowed previous episode duration: %+v", rows[0])
	}
	item.DurationSec = 120
	if err := s.SaveHistory(item); err != nil {
		t.Fatal(err)
	}
	item.DurationSec = 0
	if err := s.SaveHistory(item); err != nil {
		t.Fatal(err)
	}
	row, _ := s.GetHistoryItem(item.SeriesID)
	if row.DurationSec != 120 {
		t.Fatalf("lost same-episode duration: %+v", row)
	}
}

func TestPlaybackProgressMarksPartialEpisodesWatchedSeparatelyFromSeriesResume(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetSyncScope("user:test")

	const seriesID = "710000000000001"
	if _, err := s.SavePlaybackProgress(HistoryItem{
		SeriesID: seriesID, EpisodeNum: 1, PositionSec: 96, DurationSec: 100,
		TotalEpisodes: 112, UpdatedAt: 100,
	}, "710000000000101", "user:test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePlaybackProgress(HistoryItem{
		SeriesID: seriesID, EpisodeNum: 112, PositionSec: 3, DurationSec: 100,
		TotalEpisodes: 112, UpdatedAt: 101,
	}, "710000000000212", "user:test"); err != nil {
		t.Fatal(err)
	}

	progress, err := s.EpisodeProgress(seriesID, "user:test")
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 2 {
		t.Fatalf("episode progress = %+v, want one row per played episode", progress)
	}
	if !progress[0].Watched || !progress[1].Watched {
		t.Fatalf("episode states = %+v, want both played episodes marked watched", progress)
	}
	history, ok := s.GetHistoryItem(seriesID)
	if !ok || history.EpisodeNum != 112 || history.PositionSec != 3 {
		t.Fatalf("series resume = %+v, found=%t; want latest partial episode", history, ok)
	}

	// Rewinding does not make a viewed episode appear unplayed.
	if _, err := s.SavePlaybackProgress(HistoryItem{
		SeriesID: seriesID, EpisodeNum: 1, PositionSec: 8, DurationSec: 100,
		TotalEpisodes: 112, UpdatedAt: 102,
	}, "710000000000101", "user:test"); err != nil {
		t.Fatal(err)
	}
	progress, err = s.EpisodeProgress(seriesID, "user:test")
	if err != nil || len(progress) != 2 || !progress[0].Watched {
		t.Fatalf("rewound progress = %+v, err=%v; watched state was lost", progress, err)
	}
}
