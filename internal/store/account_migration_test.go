package store

import "testing"

func TestVerifiedAccountAliasRecoversOwnedHistoryOnly(t *testing.T) {
	s := newQueueStore(t)
	item := HistoryItem{SeriesID: "710000000000001", Title: "原有历史", UpdatedAt: 100, PositionSec: 45, EpisodeNum: 3}
	if err := s.QueueHistoryUpload(item, "710000000000101", "user:legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO episode_progress (scope, series_id, episode_num, episode_id, position_sec, duration_sec, updated_at, watched)
		VALUES ('user:legacy', ?, 1, '710000000000101', 96, 100, 100, 1)`, item.SeriesID); err != nil {
		t.Fatal(err)
	}
	other := item
	other.SeriesID = "710000000000002"
	if err := s.QueueHistoryUpload(other, "710000000000102", ""); err != nil {
		t.Fatal(err)
	}
	other.SeriesID = "710000000000003"
	if err := s.QueueHistoryUpload(other, "710000000000103", "user:other"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAuthenticatedAccount(AccountAuth{Cookies: "same-session"}, UserProfile{UserID: "canonical", LoggedIn: true}, false, "user:legacy"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetHistory()
	if err != nil || len(rows) != 1 || rows[0].PositionSec != 45 {
		t.Fatalf("recovery failed: %v %v", rows, err)
	}
	old, _ := s.PendingHistoryUploads(10, "user:legacy")
	current, _ := s.PendingHistoryUploads(10, "user:canonical")
	unowned, _ := s.PendingHistoryUploads(10, "")
	foreign, _ := s.PendingHistoryUploads(10, "user:other")
	if len(old) != 0 || len(current) != 1 || len(unowned) != 1 || len(foreign) != 1 {
		t.Fatal("queues crossed owners or were lost")
	}
	progress, err := s.EpisodeProgress(item.SeriesID, "user:canonical")
	if err != nil || len(progress) != 1 || !progress[0].Watched || progress[0].PositionSec != 96 {
		t.Fatalf("verified alias lost episode completion: %+v, err=%v", progress, err)
	}
}

func TestVerifiedAccountAliasRetainsNewerIntentAndDeletion(t *testing.T) {
	s := newQueueStore(t)
	item := HistoryItem{SeriesID: "710000000000001", UpdatedAt: 100, PositionSec: 10}
	if err := s.QueueHistoryUpload(item, "710000000000101", "user:legacy"); err != nil {
		t.Fatal(err)
	}
	item.PositionSec = 90
	if err := s.QueueHistoryUpload(item, "710000000000101", "user:canonical"); err != nil {
		t.Fatal(err)
	}
	gone := HistoryItem{SeriesID: "710000000000002", UpdatedAt: 100}
	if err := s.QueueHistoryUpload(gone, "710000000000102", "user:legacy"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHistory(gone.SeriesID, "user:canonical"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAuthenticatedAccount(AccountAuth{Cookies: "same-session"}, UserProfile{UserID: "canonical", LoggedIn: true}, false, "user:legacy"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.GetHistory()
	if len(rows) != 1 || rows[0].PositionSec != 90 {
		t.Fatalf("recovery overwrote newer progress or restored deletion: %v", rows)
	}
}

func TestVerifiedAccountAliasPreservesVisibleProgressAndClear(t *testing.T) {
	for _, cleared := range []bool{false, true} {
		t.Run(map[bool]string{false: "newer-visible-progress", true: "history-cleared"}[cleared], func(t *testing.T) {
			s := newQueueStore(t)
			item := HistoryItem{SeriesID: "710000000000001", UpdatedAt: 100, PositionSec: 10}
			if err := s.QueueHistoryUpload(item, "710000000000101", "user:legacy"); err != nil {
				t.Fatal(err)
			}
			if cleared {
				if _, err := s.db.Exec("INSERT OR REPLACE INTO history_clear_state (id,cleared_at) VALUES (1,200)"); err != nil {
					t.Fatal(err)
				}
			} else {
				item.PositionSec, item.UpdatedAt = 90, 200
				if err := s.SaveHistory(item); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetAuthenticatedAccount(AccountAuth{Cookies: "same-session"}, UserProfile{UserID: "canonical", LoggedIn: true}, false, "user:legacy"); err != nil {
				t.Fatal(err)
			}
			rows, err := s.GetHistory()
			if err != nil || (cleared && len(rows) != 0) || (!cleared && (len(rows) != 1 || rows[0].PositionSec != 90)) {
				t.Fatalf("restoration undid newer user intent: %v %v", rows, err)
			}
		})
	}
}

func TestVerifiedAccountMigrationRollsBackWithAccountWrite(t *testing.T) {
	s := newQueueStore(t)
	if err := s.SetAuthenticatedAccount(AccountAuth{Cookies: "original"}, UserProfile{UserID: "legacy", LoggedIn: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueHistoryUpload(HistoryItem{SeriesID: "710000000000001", UpdatedAt: 100}, "710000000000101", "user:legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER reject_profile BEFORE INSERT ON profile BEGIN SELECT RAISE(ABORT, 'profile write failed'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAuthenticatedAccount(AccountAuth{Cookies: "normalized"}, UserProfile{UserID: "canonical", LoggedIn: true}, false, "user:legacy"); err == nil {
		t.Fatal("expected failed account transaction")
	}
	old, _ := s.PendingHistoryUploads(10, "user:legacy")
	current, _ := s.PendingHistoryUploads(10, "user:canonical")
	rows, _ := s.GetHistory()
	if len(old) != 1 || len(current) != 0 || len(rows) != 0 || s.GetProfile().UserID != "legacy" {
		t.Fatal("failed account write partially committed migration")
	}
}
