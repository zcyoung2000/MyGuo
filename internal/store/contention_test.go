package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestPlaybackProgressWaitsForCompetingWriterOnNewConnection(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	// Pin the existing pooled connection so playback must use a new one.
	// A one-off PRAGMA during startup does not configure that new connection.
	s, err := NewStore(filepath.Join(t.TempDir(), "data & cache"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pinned, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	other, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE sync_revision_sequence SET revision=revision+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	committed := make(chan error, 1)
	time.AfterFunc(150*time.Millisecond, func() { committed <- tx.Commit() })
	item := HistoryItem{SeriesID: "710000000000001", EpisodeNum: 3, PositionSec: 35, DurationSec: 90}
	_, err = s.SavePlaybackProgress(item, "710000000000103", "")
	if commitErr := <-committed; commitErr != nil {
		t.Fatal(commitErr)
	}
	if err != nil {
		t.Fatalf("short-lived writer contention lost playback progress: %v", err)
	}
	stored, ok := s.GetHistoryItem(item.SeriesID)
	queued, queueErr := s.PendingHistoryUploads(10)
	if !ok || stored.PositionSec != 35 || queueErr != nil || len(queued) != 1 || queued[0].PositionSec != 35 {
		t.Fatalf("progress and sync intent must both commit: %+v %+v %v", stored, queued, queueErr)
	}
}

func TestStorePathWithURICharacters(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(filepath.Join(t.TempDir(), "data?#"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
