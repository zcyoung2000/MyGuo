package library

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"myguo/internal/sanitize"
)

func (s *Service) GetHistory(ctx context.Context) ([]HistoryItem, error) {
	statusErr := s.loggedInHistoryStatusError()
	s.historyMu.Lock()
	if len(s.cachedHistory) > 0 && time.Since(s.historyCachedAt) < 30*time.Second {
		cached := make([]HistoryItem, len(s.cachedHistory))
		copy(cached, s.cachedHistory)
		s.historyMu.Unlock()
		return sanitize.Sanitize(cached), statusErr
	}

	localItems, err := s.store.GetHistory()
	if err != nil {
		s.historyMu.Unlock()
		return nil, err
	}
	if len(localItems) > 0 {
		for i := range localItems {
			localItems[i].CoverURL = s.imageURL(localItems[i].CoverURL)
		}
		localItems = sanitize.Sanitize(localItems)
		s.cachedHistory = localItems
		shouldSync := s.historyCachedAt.IsZero() || time.Since(s.historyCachedAt) >= 60*time.Second
		s.historyCachedAt = time.Now()
		generation := s.historyGeneration
		if shouldSync && !s.historySyncing {
			s.historySyncing = true
			s.background.Go(func(ctx context.Context) {
				defer func() {
					s.historyMu.Lock()
					s.historySyncing = false
					s.historyMu.Unlock()
				}()
				if err := s.syncRemoteHistory(ctx, generation); err != nil {
					log.Printf("[history] background sync failed: %v", err)
				}
			})
		}
		s.historyMu.Unlock()
		return sanitize.Sanitize(localItems), statusErr
	}
	s.historyMu.Unlock()

	if s.currentSyncSession().cookies != "" {
		if err := s.syncRemoteHistory(ctx, s.currentHistoryGeneration()); err != nil {
			items, readErr := s.historyFromStore()
			if readErr != nil {
				return nil, errors.Join(err, readErr)
			}
			return items, errors.Join(err, s.loggedInHistoryStatusError())
		}
	}

	items, err := s.historyFromStore()
	return items, errors.Join(err, s.loggedInHistoryStatusError())
}

func (s *Service) loggedInHistoryStatusError() error {
	if session := s.currentSyncSession(); session.cookies != "" {
		return s.historySyncStatusError()
	}
	return nil
}

func (s *Service) RefreshHistory(ctx context.Context) ([]HistoryItem, error) {
	if cookies, _ := s.credentials(); cookies == "" {
		return s.GetHistory(ctx)
	}
	generation := s.currentHistoryGeneration()
	if err := s.cleanupLegacyHistoryMutations(); err != nil {
		return nil, err
	}
	uploadDone := make(chan error, 1)
	s.background.Go(func(context.Context) {
		_, err := s.syncPendingHistoryMutations()
		uploadDone <- err
	})
	syncErr := s.syncRemoteHistory(ctx, generation)
	uploadTimer := time.NewTimer(250 * time.Millisecond)
	select {
	case uploadErr := <-uploadDone:
		if uploadErr != nil {
			log.Printf("[history] progress upload remains queued: %v", uploadErr)
		}
	case <-uploadTimer.C:
	}
	if !uploadTimer.Stop() {
		select {
		case <-uploadTimer.C:
		default:
		}
	}
	items, readErr := s.historyFromStore()
	return items, errors.Join(syncErr, readErr, s.historySyncStatusError())
}

func (s *Service) historySyncStatusError() error {
	s.historyMutationMu.Lock()
	mutationErr := s.historyMutationErr
	s.historyMutationMu.Unlock()
	if s.store != nil {
		scope := s.currentSyncSession().scope
		if pending, err := s.store.PendingOwnedHistoryMutationIDs(scope); err != nil {
			mutationErr = errors.Join(mutationErr, err)
		} else if len(pending) > 0 {
			mutationErr = errors.Join(mutationErr, fmt.Errorf("%d 条本机播放进度待上传", len(pending)))
		}
	}
	return mutationErr
}

func (s *Service) cleanupLegacyHistoryMutations() error {
	count, err := s.store.CleanupLegacyHistoryMutations()
	if err != nil {
		return fmt.Errorf("清理旧版历史同步任务失败: %w", err)
	}
	if count > 0 {
		log.Printf("[history] retired %d unowned legacy sync tasks; local history retained", count)
	}
	return nil
}

func (s *Service) historyFromStore() ([]HistoryItem, error) {
	list, err := s.store.GetHistory()
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].CoverURL = s.imageURL(list[i].CoverURL)
	}
	list = sanitize.Sanitize(list)
	s.historyMu.Lock()
	s.cachedHistory = append([]HistoryItem(nil), list...)
	s.historyCachedAt = time.Now()
	s.historyMu.Unlock()
	return sanitize.Sanitize(list), nil
}
