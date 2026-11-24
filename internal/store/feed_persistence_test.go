package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestRecentFeedWindowUsesCountAndSurvivesRestartAndSessionRotation(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	var ids []string
	for i := 0; i < 201; i++ {
		ids = append(ids, fmt.Sprintf("series-%03d", i))
	}
	if err := s.SaveFeedSeenIDs("推荐", 1, ids); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := s.StartNextFeedGeneration("推荐"); err != nil {
			t.Fatal(err)
		}
	}
	// Refresh an older retained ID and add one new ID. Insertion order, not
	// session number or timestamp ties, must evict series-001, not series-002.
	if err := s.SaveFeedSeenIDs("推荐", 11, []string{"series-002", "fresh"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen, err := s.GetRecentFeedSeenIDs("推荐")
	if err != nil || len(seen) != 200 || seen["series-000"] || seen["series-001"] || !seen["series-002"] || !seen["fresh"] {
		t.Fatalf("recent window after restart = %v, %v", seen, err)
	}
}

func TestPendingFeedOutsideRecentWindowRemainsDeduplicatedAndConsumptionRefreshesIt(t *testing.T) {
	s := newFeedPersistenceStore(t)
	if err := s.AppendFeedQueue("推荐", []FeedQueueEntry{{SeriesID: "pending", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 205; i++ {
		ids = append(ids, fmt.Sprintf("series-%03d", i))
	}
	if err := s.SaveFeedSeenIDs("推荐", 1, ids); err != nil {
		t.Fatal(err)
	}
	seen, err := s.GetRecentFeedSeenIDs("推荐")
	if err != nil || !seen["pending"] {
		t.Fatalf("pending card fell out of exclusion set: %v", err)
	}
	if err := s.ConsumeFeedQueue("推荐", "pending"); err != nil {
		t.Fatal(err)
	}
	seen, err = s.GetRecentFeedSeenIDs("推荐")
	queue, queueErr := s.GetFeedQueue("推荐")
	if err != nil || queueErr != nil || len(queue) != 0 || !seen["pending"] || len(seen) != 200 {
		t.Fatalf("consumption lost its recent ID: %v / %v / %v", seen, err, queueErr)
	}
}

func TestRecommendPageFailureRollsBackCursorRecentIDsAndQueue(t *testing.T) {
	s := newFeedPersistenceStore(t)
	if err := s.SaveRecommendPage(context.Background(), "推荐", "old", 1,
		[]FeedQueueEntry{{SeriesID: "old", Payload: json.RawMessage(`{}`)}}, []string{"old"}); err != nil {
		t.Fatal(err)
	}
	// Fail after pending rows have been inserted, while recent IDs are saved.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_recent BEFORE INSERT ON feed_seen
		WHEN NEW.series_id = 'new' BEGIN SELECT RAISE(ABORT, 'fixture write failure'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.SaveRecommendPage(context.Background(), "推荐", "new-cursor", 2,
		[]FeedQueueEntry{{SeriesID: "new", Payload: json.RawMessage(`{}`)}}, []string{"new"})
	if err == nil {
		t.Fatal("page should fail")
	}
	progress, _ := s.GetFeedProgress("推荐")
	queue, err := s.GetFeedQueue("推荐")
	seen, seenErr := s.GetRecentFeedSeenIDs("推荐")
	if err != nil || seenErr != nil || progress.Cursor != "old" || progress.Generation != 1 || len(queue) != 1 || queue[0].SeriesID != "old" || seen["new"] {
		t.Fatalf("failed page partially committed: %+v / %+v / %v / %v", progress, queue, seen, err)
	}
}

func TestFeedConsumptionFailureKeepsPendingTailAndCanRetry(t *testing.T) {
	s := newFeedPersistenceStore(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.AppendFeedQueue("推荐", []FeedQueueEntry{{SeriesID: id, Payload: json.RawMessage(`{}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_recent BEFORE INSERT ON feed_seen
		BEGIN SELECT RAISE(ABORT, 'fixture write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeFeedQueue("推荐", "b"); err == nil {
		t.Fatal("consumption should fail")
	}
	queue, err := s.GetFeedQueue("推荐")
	if err != nil || len(queue) != 3 {
		t.Fatalf("failed consumption lost pending cards: %+v / %v", queue, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_recent`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.ConsumeFeedQueueContext(ctx, "推荐", "b"); err == nil {
		t.Fatal("canceled write should fail")
	}
	if err := s.ConsumeFeedQueue("推荐", "b"); err != nil {
		t.Fatal(err)
	}
	queue, err = s.GetFeedQueue("推荐")
	if err != nil || len(queue) != 1 || queue[0].SeriesID != "c" {
		t.Fatalf("retry consumed the wrong tail: %+v / %v", queue, err)
	}
}

func newFeedPersistenceStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
