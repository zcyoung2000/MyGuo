package store

import "testing"

func TestEpisodeProgressRecognizesLegacyPartialPlaybackWithoutDuration(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	const seriesID = "710000000000001"
	if _, err := s.db.Exec(`INSERT INTO episode_progress
		(scope, series_id, episode_num, episode_id, position_sec, duration_sec, updated_at, watched)
		VALUES ('user:test', ?, 8, '', 0.5, 0, 100, 0)`, seriesID); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	progress, err := s.EpisodeProgress(seriesID, "user:test")
	if err != nil || len(progress) != 1 || !progress[0].Watched || progress[0].PositionSec != .5 {
		t.Fatalf("legacy partial episode not recognized after restart: %+v, %v", progress, err)
	}
	other, err := s.EpisodeProgress(seriesID, "user:other")
	if err != nil || len(other) != 0 {
		t.Fatalf("episode state crossed accounts: %+v, %v", other, err)
	}
}

func TestEpisodeSnapshotKeepsEarlierViewedEpisodesForSurvivingSeries(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetSyncScope("user:test")
	const keep, removed = "710000000000001", "710000000000002"
	if _, err := s.SaveEpisodeProgressBatchIfCurrentScope([]EpisodeProgress{
		{SeriesID: keep, EpisodeNum: 1, PositionSec: 3, UpdatedAt: 100},
		{SeriesID: keep, EpisodeNum: 8, PositionSec: 7, UpdatedAt: 101},
		{SeriesID: removed, EpisodeNum: 1, PositionSec: 10, UpdatedAt: 100},
	}, "user:test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceHistoryBatchIfCurrentScope([]HistoryItem{
		{SeriesID: keep, EpisodeNum: 8, PositionSec: 12, UpdatedAt: 102},
	}, "user:test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceEpisodeProgressIfCurrentScope([]EpisodeProgress{
		{SeriesID: keep, EpisodeNum: 8, PositionSec: 12, UpdatedAt: 102},
	}, "user:test"); err != nil {
		t.Fatal(err)
	}
	progress, err := s.EpisodeProgress(keep, "user:test")
	if err != nil || len(progress) != 2 || !progress[0].Watched || !progress[1].Watched || progress[1].PositionSec != 12 {
		t.Fatalf("cloud's latest episode erased earlier local evidence: %+v, %v", progress, err)
	}
	progress, err = s.EpisodeProgress(removed, "user:test")
	if err != nil || len(progress) != 0 {
		t.Fatalf("deleted series retained episode state: %+v, %v", progress, err)
	}
}

func TestZeroProgressDoesNotMarkAnEpisodeWatched(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const seriesID = "710000000000001"
	if _, err := s.SavePlaybackProgress(HistoryItem{SeriesID: seriesID, EpisodeNum: 1, DurationSec: 100}, "episode-1", ""); err != nil {
		t.Fatal(err)
	}
	progress, err := s.EpisodeProgress(seriesID)
	if err != nil || len(progress) != 0 {
		t.Fatalf("unplayed episode was marked watched: %+v, %v", progress, err)
	}
}
