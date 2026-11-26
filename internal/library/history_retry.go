package library

import (
	"fmt"
	"log"
	"time"

	userstore "myguo/internal/store"
)

// schedulePendingHistorySync arms one wake-up for durable work. Timers are not
// members of background: shutdown must leave failed work queued, not wait for
// a network cooldown. NewService resumes that work on the next launch.
func (s *Service) schedulePendingHistorySync() {
	s.historyRetryMu.Lock()
	defer s.historyRetryMu.Unlock()
	if s.historyRetryClosed || s.store == nil {
		return
	}
	s.historyRetryEpoch++
	if s.historyRetryTimer != nil {
		s.historyRetryTimer.Stop()
		s.historyRetryTimer = nil
	}
	session := s.currentSyncSession()
	if session.cookies == "" {
		return
	}
	now := time.Now()
	var next time.Time
	consider := func(item userstore.PendingHistoryUpload) {
		at := s.historyUploadNextAttemptLocked(item)
		if at.IsZero() {
			s.historyMutationMu.Lock()
			failed := s.historyMutationErr != nil
			s.historyMutationMu.Unlock()
			if failed {
				// Unprocessed work (including a failed database read/ack) must not
				// spin. Network failures already have their own bounded backoff.
				at = now.Add(historyUploadBaseBackoff)
			} else {
				at = now
			}
		}
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	for offset := 0; ; offset += userSyncPageSize {
		items, err := s.store.PendingHistoryUploadsPage(userSyncPageSize, offset, session.scope)
		if err != nil {
			log.Printf("[history] retry queue read failed: %v", err)
			// A transient database failure must not strand the queue either.
			next = now.Add(historyUploadBaseBackoff)
			break
		}
		for _, item := range items {
			consider(item)
		}
		if len(items) < userSyncPageSize {
			break
		}
	}
	deletes, err := s.store.PendingHistoryDeletes(50, session.scope)
	if err != nil {
		next = now.Add(historyUploadBaseBackoff)
	} else {
		for _, item := range deletes {
			consider(userstore.PendingHistoryUpload{HistoryItem: item.HistoryItem, Scope: item.Scope})
		}
	}
	if next.IsZero() || !s.syncSessionCurrent(session) {
		return
	}
	epoch := s.historyRetryEpoch
	s.historyRetryTimer = time.AfterFunc(time.Until(next), func() {
		s.historyRetryMu.Lock()
		current := !s.historyRetryClosed && epoch == s.historyRetryEpoch
		if current {
			s.historyRetryTimer = nil
		}
		s.historyRetryMu.Unlock()
		if current && s.syncSessionCurrent(session) {
			s.triggerPendingHistorySync()
		}
	})
}

func historyUploadKey(item userstore.PendingHistoryUpload) string {
	// A bad episode must not delay other episodes of the same series. New
	// revisions of that episode still respect the existing failure cooldown.
	return fmt.Sprintf("%s\x00%s\x00%d", item.Scope, item.SeriesID, item.EpisodeNum)
}

// Caller holds historyRetryMu. Account throttling applies to every video and
// legacy deletion on that identity, rather than just the rejected batch.
func (s *Service) historyUploadNextAttemptLocked(item userstore.PendingHistoryUpload) time.Time {
	next := s.historyUploadRetries[historyUploadKey(item)].nextAt
	if scope := s.historyScopeRetries[item.Scope].nextAt; scope.After(next) {
		next = scope
	}
	return next
}

func (s *Service) historyUploadReady(item userstore.PendingHistoryUpload) bool {
	s.historyRetryMu.Lock()
	defer s.historyRetryMu.Unlock()
	return !time.Now().Before(s.historyUploadNextAttemptLocked(item))
}
