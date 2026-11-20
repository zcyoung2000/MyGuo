package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newQueueStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHistoryAckKeepsNewerProgressWithSameTimestamp(t *testing.T) {
	s := newQueueStore(t)
	item := HistoryItem{SeriesID: "710000000000001", UpdatedAt: 100, PositionSec: 10}
	if err := s.QueueHistoryUpload(item, "710000000000101", "A"); err != nil {
		t.Fatal(err)
	}
	old, err := s.PendingHistoryUploads(10, "A")
	if err != nil || len(old) != 1 {
		t.Fatalf("snapshot: %v, %v", old, err)
	}
	item.PositionSec = 20
	if err := s.QueueHistoryUpload(item, "710000000000102", "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.AckHistoryUploads(old); err != nil {
		t.Fatal(err)
	}
	newer, err := s.PendingHistoryUploads(10, "A")
	if err != nil || len(newer) != 1 || newer[0].PositionSec != 20 || newer[0].EpisodeID != "710000000000102" {
		t.Fatalf("newer progress lost: %v, %v", newer, err)
	}
	if err := s.AckHistoryUploads(newer); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.PendingHistoryUploads(10, "A")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("confirmed progress not removed: %v, %v", remaining, err)
	}
}

func TestFavoriteAckDoesNotCrossOwnersOrReinsertedIntent(t *testing.T) {
	s := newQueueStore(t)
	const id = "710000000000001"
	if err := s.QueueFavoriteOp(id, true, "A"); err != nil {
		t.Fatal(err)
	}
	old, _ := s.PendingFavoriteOps(10, "A")
	if err := s.DeletePendingFavoriteOps([]string{id}, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueFavoriteOp(id, true, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueFavoriteOp(id, false, "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.AckFavoriteOps(old); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"A", "B"} {
		items, err := s.PendingFavoriteOps(10, owner)
		if err != nil || len(items) != 1 || items[0].Scope != owner || items[0].Added != (owner == "A") {
			t.Fatalf("owner %s intent changed: %v, %v", owner, items, err)
		}
	}
}

func TestHistoryDeleteAckKeepsADeleteQueuedAfterReplay(t *testing.T) {
	s := newQueueStore(t)
	item := HistoryItem{SeriesID: "710000000000001", UpdatedAt: 100, EpisodeNum: 1}
	if err := s.SaveHistory(item, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHistory(item.SeriesID, "A"); err != nil {
		t.Fatal(err)
	}
	old, _ := s.PendingHistoryDeletes(10, "A")
	if err := s.SaveHistory(item, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHistory(item.SeriesID, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.AckHistoryDeletes(old); err != nil {
		t.Fatal(err)
	}
	items, err := s.PendingHistoryDeletes(10, "A")
	if err != nil || len(items) != 1 || items[0].Revision <= old[0].Revision {
		t.Fatalf("new delete lost: %v, %v", items, err)
	}
}

func TestQueueMigrationKeepsLegacyDataUnownedAndRevisionSurvivesRestart(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ToggleFavorite(FavoriteItem{SeriesID: "710000000000001", Title: "Local"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "userdata.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, queue := range syncQueues {
		fields := strings.Replace(queue.fields, "series_id TEXT NOT NULL", "series_id TEXT PRIMARY KEY", 1)
		if _, err := db.Exec("DROP TABLE " + queue.name + "; CREATE TABLE " + queue.name + " (" + fields + ")"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO pending_favorite_ops (series_id, added, updated_at) VALUES ('710000000000001', 1, 100)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	unowned, err := s.PendingFavoriteOps(10, "")
	if err != nil || len(unowned) != 1 {
		t.Fatalf("legacy intent lost: %v, %v", unowned, err)
	}
	other, err := s.PendingFavoriteOps(10, "B")
	if err != nil || len(other) != 0 || !s.IsFavorite("710000000000001") {
		t.Fatal("migration assigned old data to a new identity or removed local favorite")
	}
	if err := s.QueueFavoriteOp("710000000000001", true, "A"); err != nil {
		t.Fatal(err)
	}
	old, _ := s.PendingFavoriteOps(10, "A")
	if err := s.AckFavoriteOps(old); err != nil {
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
	if err := s.QueueFavoriteOp("710000000000001", true, "A"); err != nil {
		t.Fatal(err)
	}
	if err := s.AckFavoriteOps(old); err != nil {
		t.Fatal(err)
	}
	newer, err := s.PendingFavoriteOps(10, "A")
	if err != nil || len(newer) != 1 || newer[0].Revision <= old[0].Revision {
		t.Fatalf("restart reused acknowledged revision: %v, %v", newer, err)
	}
}

func TestNativeProgressAndSyncIntentCommitTogether(t *testing.T) {
	s := newQueueStore(t)
	s.SetSyncScope("A")
	item := HistoryItem{SeriesID: "710000000000001", EpisodeNum: 1, PositionSec: 10, DurationSec: 200, Title: "preserved"}
	if _, err := s.SavePlaybackProgress(item, "710000000000101", "A"); err != nil {
		t.Fatal(err)
	}
	// Simulate an outbox write failure after the visible clock update.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_native_upload BEFORE INSERT ON pending_history_uploads BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	item.PositionSec = 99
	if _, err := s.SavePlaybackProgress(item, "710000000000101", "A"); err == nil {
		t.Fatal("outbox failure was ignored")
	}
	stored, _ := s.GetHistoryItem(item.SeriesID)
	pending, _ := s.PendingHistoryUploads(10, "A")
	if stored.PositionSec != 10 || len(pending) != 1 || pending[0].PositionSec != 10 {
		t.Fatalf("partial write: local=%+v pending=%+v", stored, pending)
	}
}
func TestNativeMutationsRejectOldOwnerUnderStoreLock(t *testing.T) {
	s := newQueueStore(t)
	s.SetSyncScope("B")
	const id = "710000000000001"
	if _, err := s.SavePlaybackProgress(HistoryItem{SeriesID: id, PositionSec: 80}, "710000000000101", "A"); err == nil {
		t.Fatal("stale progress accepted")
	}
	if _, err := s.ToggleFavorite(FavoriteItem{SeriesID: id}, "A"); err == nil {
		t.Fatal("stale favorite accepted")
	}
	if err := s.SetLike(LikeItem{SeriesID: id, EpisodeID: "710000000000101"}, true, "A"); err == nil {
		t.Fatal("stale like accepted")
	}
	if _, ok := s.GetHistoryItem(id); ok || s.IsFavorite(id) || s.IsLiked(id, "710000000000101", 1) {
		t.Fatal("old mutation appeared in current account")
	}
}

func TestLegacyHistoryCleanupRetiresTasksAndRetainsOnlyLocalProgress(t *testing.T) {
	s := newQueueStore(t)
	s.SetSyncScope("user:A")
	const localID, orphanID, ownedID, deletedID = "710000000000001", "710000000000002", "710000000000003", "710000000000004"
	local := HistoryItem{SeriesID: localID, Title: "本地历史", EpisodeNum: 4, PositionSec: 90, UpdatedAt: 300, ContentType: 1004}
	if err := s.SaveHistory(local, "user:A"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{localID, orphanID} {
		if err := s.QueueHistoryUpload(HistoryItem{SeriesID: id, EpisodeNum: 2, PositionSec: 20, UpdatedAt: 200}, "710000000000102", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveHistory(HistoryItem{SeriesID: deletedID, EpisodeNum: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHistory(deletedID, ""); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"user:A", "user:B"} {
		if err := s.QueueHistoryUpload(HistoryItem{SeriesID: ownedID, EpisodeNum: 1, PositionSec: 30}, "710000000000301", scope); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := s.CleanupLegacyHistoryMutations(); err != nil || count != 3 {
		t.Fatalf("retired tasks = %d, %v; want two uploads and one delete", count, err)
	}
	if count, err := s.PendingUnownedHistoryMutationCount(); err != nil || count != 0 {
		t.Fatalf("unowned tasks remain: count=%d err=%v", count, err)
	}
	for _, scope := range []string{"user:A", "user:B"} {
		pending, err := s.PendingHistoryUploads(10, scope)
		if err != nil || len(pending) != 1 || pending[0].SeriesID != ownedID {
			t.Fatalf("owned task changed for %s: %+v, %v", scope, pending, err)
		}
	}
	for range 2 {
		if err := s.ReplaceHistoryBatch(nil, "user:A"); err != nil {
			t.Fatal(err)
		}
		got, exists := s.GetHistoryItem(localID)
		if !exists || got != local {
			t.Fatalf("cleanup lost visible progress: %+v, exists=%t", got, exists)
		}
	}
	if count, err := s.CleanupLegacyHistoryMutations(); err != nil || count != 0 {
		t.Fatalf("cleanup was not idempotent: count=%d err=%v", count, err)
	}
	// New playback establishes the active account's ownership normally.
	if _, err := s.SavePlaybackProgress(local, "710000000000104", "user:A"); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM local_history_retention`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("fresh playback left local-only retention: count=%d err=%v", retained, err)
	}
	if err := s.SignOut("device"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.GetHistory(); err != nil || len(rows) != 0 {
		t.Fatalf("retained history leaked across signout: %+v, %v", rows, err)
	}
}

func TestLegacyHistoryCleanupRollsBackRetentionWhenRetirementFails(t *testing.T) {
	s := newQueueStore(t)
	const id = "710000000000001"
	if err := s.SaveHistory(HistoryItem{SeriesID: id, PositionSec: 45}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueHistoryUpload(HistoryItem{SeriesID: id}, "710000000000101", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_retirement BEFORE DELETE ON pending_history_uploads BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupLegacyHistoryMutations(); err == nil {
		t.Fatal("cleanup ignored retirement failure")
	}
	var retained int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM local_history_retention`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("partial retention committed: count=%d err=%v", retained, err)
	}
	if count, err := s.PendingUnownedHistoryMutationCount(); err != nil || count != 1 {
		t.Fatalf("failed cleanup lost intent: count=%d err=%v", count, err)
	}
}

func TestLegacyHistoryCleanupRunsOnOpenAndSurvivesRestart(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	const id = "710000000000001"
	if err := s.SaveHistory(HistoryItem{SeriesID: id, Title: "保留进度", PositionSec: 45}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueHistoryUpload(HistoryItem{SeriesID: id}, "710000000000101", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s, err = NewStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := s.PendingUnownedHistoryMutationCount(); err != nil || count != 0 {
			t.Fatalf("startup did not clean legacy tasks: %d, %v", count, err)
		}
		if err := s.ReplaceHistoryBatch(nil); err != nil {
			t.Fatal(err)
		}
		if got, ok := s.GetHistoryItem(id); !ok || got.PositionSec != 45 {
			t.Fatalf("retained clock lost on reopen: %+v, %t", got, ok)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyHistoryRetentionIsClearedOnAccountChange(t *testing.T) {
	s := newQueueStore(t)
	const id = "710000000000001"
	if err := s.SaveHistory(HistoryItem{SeriesID: id, PositionSec: 45}); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueHistoryUpload(HistoryItem{SeriesID: id}, "710000000000101", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupLegacyHistoryMutations(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAuthenticatedAccount(AccountAuth{Cookies: "sessionid=B"}, UserProfile{UserID: "B", LoggedIn: true}, true); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM local_history_retention`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("local retention survived account change: count=%d err=%v", retained, err)
	}
	if rows, err := s.GetHistory(); err != nil || len(rows) != 0 {
		t.Fatalf("old history leaked into new account: %+v, %v", rows, err)
	}
}

func TestNativeFavoriteAndIntentRollbackTogether(t *testing.T) {
	s := newQueueStore(t)
	s.SetSyncScope("A")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_native_favorite BEFORE INSERT ON pending_favorite_ops BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	const id = "710000000000001"
	if _, err := s.ToggleFavorite(FavoriteItem{SeriesID: id}, "A"); err == nil {
		t.Fatal("favorite intent failure ignored")
	}
	if s.IsFavorite(id) {
		t.Fatal("local favorite committed without its sync intent")
	}
}

func TestHistoryMetadataQueueScopesRetriesAndAcknowledges(t *testing.T) {
	s := newQueueStore(t)
	s.SetSyncScope("account-A")
	if err := s.QueueHistoryMetadata([]string{"710000000000001", "710000000000002"}, "account-A"); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueHistoryMetadata([]string{"710000000000003"}, "account-B"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pending, err := s.PendingHistoryMetadata(10, "account-A", now)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending A = %+v, %v", pending, err)
	}
	if err := s.RetryHistoryMetadata(pending[:1], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	next, hasNext, err := s.NextHistoryMetadataAttempt("account-A")
	if err != nil || !hasNext || next.Unix() != now.Unix() {
		t.Fatalf("next due attempt = %v/%t, %v; want the immediately due second job", next, hasNext, err)
	}
	if err := s.AckHistoryMetadata(pending[1:]); err != nil {
		t.Fatal(err)
	}
	due, err := s.PendingHistoryMetadata(10, "account-A", now)
	if err != nil || len(due) != 0 {
		t.Fatalf("retried job became immediately due: %+v, %v", due, err)
	}
	next, hasNext, err = s.NextHistoryMetadataAttempt("account-A")
	if err != nil || !hasNext || next.Before(now.Add(59*time.Second)) {
		t.Fatalf("backoff next attempt = %v/%t, %v", next, hasNext, err)
	}
	if count, err := s.PendingHistoryMetadataCount("account-B"); err != nil || count != 0 {
		t.Fatalf("foreign account metadata queue visible: count=%d err=%v", count, err)
	}
}

func TestHistoryMetadataEnrichmentPreservesProgressAndRealMetadata(t *testing.T) {
	s := newQueueStore(t)
	s.SetSyncScope("account-A")
	const id = "710000000000001"
	local := HistoryItem{SeriesID: id, Title: "第3集", CoverURL: "", EpisodeNum: 3, EpisodeTitle: "第3集", PositionSec: 77, DurationSec: 0, TotalEpisodes: 10, UpdatedAt: 100}
	if err := s.SaveHistory(local, "account-A"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.EnrichHistoryMetadataIfCurrentScope(id, HistoryItem{
		SeriesID: id, Title: "真实剧名", CoverURL: "https://img.invalid/cover.jpg", Intro: "简介", ContentType: 1,
		TotalEpisodes: 10, EpisodeNum: 3, DurationSec: 120,
	}, "account-A")
	if err != nil || !updated {
		t.Fatalf("enrichment = %t, %v", updated, err)
	}
	got, ok := s.GetHistoryItem(id)
	if !ok || got.Title != "真实剧名" || got.CoverURL != "https://img.invalid/cover.jpg" || got.PositionSec != 77 || got.UpdatedAt != 100 || got.DurationSec != 120 {
		t.Fatalf("enrichment row = %+v, exists=%t", got, ok)
	}
}

func TestUnavailableHistoryMetadataIsVisibleAndRecheckedAfterDeadline(t *testing.T) {
	s := newQueueStore(t)
	s.SetSyncScope("account-A")
	const id = "710000000000001"
	if err := s.QueueHistoryMetadata([]string{id}, "account-A"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingHistoryMetadata(10, "account-A", time.Now())
	if err != nil || len(pending) != 1 {
		t.Fatalf("initial metadata queue = %+v, %v", pending, err)
	}
	if err := s.MarkHistoryMetadataUnavailable(pending[0], "该剧集已下架", time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if count, err := s.PendingHistoryMetadataCount("account-A"); err != nil || count != 0 {
		t.Fatalf("unavailable metadata remains in retry queue: count=%d err=%v", count, err)
	}
	if count, err := s.UnavailableHistoryMetadataCount("account-A"); err != nil || count != 1 {
		t.Fatalf("unavailable metadata status count=%d err=%v", count, err)
	}
	if err := s.QueueHistoryMetadata([]string{id}, "account-A"); err != nil {
		t.Fatal(err)
	}
	if count, err := s.PendingHistoryMetadataCount("account-A"); err != nil || count != 0 {
		t.Fatalf("permanent content error retried early: count=%d err=%v", count, err)
	}
	if _, err := s.db.Exec(`UPDATE unavailable_history_metadata SET retry_after = 0 WHERE scope = ? AND series_id = ?`, "account-A", id); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueHistoryMetadata([]string{id}, "account-A"); err != nil {
		t.Fatal(err)
	}
	if count, err := s.PendingHistoryMetadataCount("account-A"); err != nil || count != 1 {
		t.Fatalf("expired unavailable record was not rechecked: count=%d err=%v", count, err)
	}
	if count, err := s.UnavailableHistoryMetadataCount("account-A"); err != nil || count != 0 {
		t.Fatalf("rechecked record still marked unavailable: count=%d err=%v", count, err)
	}
}
