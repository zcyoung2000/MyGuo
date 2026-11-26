package library

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/mediautil"
	userstore "myguo/internal/store"
)

const (
	userSyncPageSize = 20
)

var errParameterInvalid = hgapi.ErrParameterInvalid

type historyUploadRetry struct {
	attempt int
	nextAt  time.Time
}

const (
	historyUploadBaseBackoff = 5 * time.Second
	historyUploadMaxBackoff  = 10 * time.Minute
	invalidHistoryMaxBackoff = 30 * time.Minute
)

func apiProgress(item HistoryItem) hgapi.ProgressRecord {
	return hgapi.ProgressRecord{SeriesID: item.SeriesID, EpisodeNum: item.EpisodeNum, PositionSec: item.PositionSec, DurationSec: item.DurationSec, TotalEpisodes: item.TotalEpisodes}
}
func apiProgressBatch(items []HistoryItem) []hgapi.ProgressRecord {
	out := make([]hgapi.ProgressRecord, len(items))
	for i, item := range items {
		out[i] = apiProgress(item)
	}
	return out
}
func (s *Service) resolveHistoryEpisodeID(item HistoryItem) string {
	if cached, ok := s.store.GetCachedEpisodeAt(item.SeriesID, "", item.EpisodeNum); ok {
		return cached.EpisodeID
	}
	ctx, cancel := context.WithTimeout(s.background.Context(), 8*time.Second)
	defer cancel()
	episodes, err := s.episodes(ctx, item.SeriesID)
	if err != nil {
		return ""
	}
	for _, episode := range mediautil.PageItems(episodes) {
		if episode.Number == item.EpisodeNum {
			return episode.ID
		}
	}
	return ""
}

func (s *Service) reportPlaybackProgressBatch(ctx context.Context, items []HistoryItem, episodeIDs []string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Playback clocks belong to read_progress/upload. read_history/update is
	// retained for deletion compatibility and rejects valid progress records on
	// some hosts, so do not send every save through both mutation endpoints.
	return s.uploadProgress(ctx, items, episodeIDs)
}

func (s *Service) triggerPendingHistorySync() {
	s.background.Go(func(context.Context) {
		if _, err := s.syncPendingHistoryMutations(); err != nil {
			log.Printf("[history] pending sync failed: %v", err)
		}
	})
}

func (s *Service) syncPendingHistoryMutations() (int, error) {
	s.historyMutationMu.Lock()
	if s.historyMutationSyncing {
		s.historyMutationWake = true
		s.historyMutationMu.Unlock()
		return 0, nil
	}
	s.historyMutationSyncing = true
	s.historyMutationMu.Unlock()
	defer s.schedulePendingHistorySync()

	processed := 0
	for {
		// Upload first, then delete. If a save is followed immediately by a
		// delete, this preserves the operation order on the server. A delete
		// followed by a save removes its delete intent while saving.
		uploaded, err := s.processPendingHistoryUploads()
		processed += uploaded
		if err != nil {
			s.historyMutationMu.Lock()
			s.historyMutationSyncing = false
			s.historyMutationErr = err
			s.historyMutationMu.Unlock()
			if processed > 0 {
				s.touchHistoryMutation()
			}
			return processed, err
		}
		deleted, err := s.processPendingHistoryDeletes()
		processed += deleted
		if err != nil {
			s.historyMutationMu.Lock()
			s.historyMutationSyncing = false
			s.historyMutationErr = err
			s.historyMutationMu.Unlock()
			if processed > 0 {
				s.touchHistoryMutation()
			}
			return processed, err
		}
		s.historyMutationMu.Lock()
		again := s.historyMutationWake || uploaded > 0 || deleted > 0
		s.historyMutationWake = false
		if !again {
			s.historyMutationSyncing = false
			s.historyMutationErr = nil
			s.historyMutationMu.Unlock()
			if processed > 0 {
				s.touchHistoryMutation()
			}
			return processed, nil
		}
		s.historyMutationMu.Unlock()
	}
}

func (s *Service) markHistoryUploadFailed(items []userstore.PendingHistoryUpload, err error) {
	s.historyRetryMu.Lock()
	defer s.historyRetryMu.Unlock()
	if s.historyUploadRetries == nil {
		s.historyUploadRetries = make(map[string]historyUploadRetry)
	}
	if errors.Is(err, hgapi.ErrRateLimited) {
		if s.historyScopeRetries == nil {
			s.historyScopeRetries = make(map[string]historyUploadRetry)
		}
		scopes := make(map[string]bool)
		for _, item := range items {
			if !scopes[item.Scope] {
				retry := s.historyScopeRetries[item.Scope]
				retry.attempt++
				retry.nextAt = time.Now().Add(min(time.Minute<<min(retry.attempt-1, 5), historyUploadMaxBackoff))
				s.historyScopeRetries[item.Scope] = retry
				scopes[item.Scope] = true
			}
		}
	}
	base := historyUploadBaseBackoff
	if errors.Is(err, errParameterInvalid) || errors.Is(err, hgapi.ErrRateLimited) {
		base = time.Minute
	}
	for _, item := range items {
		key := historyUploadKey(item)
		retry := s.historyUploadRetries[key]
		retry.attempt++
		delay := base << min(retry.attempt-1, 5)
		if delay < historyUploadBaseBackoff {
			delay = historyUploadBaseBackoff
		}
		if errors.Is(err, errParameterInvalid) {
			delay = min(delay, invalidHistoryMaxBackoff)
		} else {
			delay = min(delay, historyUploadMaxBackoff)
		}
		retry.nextAt = time.Now().Add(delay)
		s.historyUploadRetries[key] = retry
	}
}

func (s *Service) processPendingHistoryUploads() (int, error) {
	ctx := s.bindSyncSession(s.background.Context())
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return 0, nil
	}
	due := make([]userstore.PendingHistoryUpload, 0, userSyncPageSize)
	for offset := 0; len(due) < userSyncPageSize; offset += userSyncPageSize {
		items, err := s.store.PendingHistoryUploadsPage(userSyncPageSize, offset, session.scope)
		if err != nil {
			return 0, err
		}
		for _, pending := range items {
			if s.historyUploadReady(pending) {
				due = append(due, pending)
				if len(due) == userSyncPageSize {
					break
				}
			}
		}
		if len(items) < userSyncPageSize {
			break
		}
	}
	if len(due) == 0 {
		return 0, nil
	}

	valid := make([]HistoryItem, 0, len(due))
	ack := make([]userstore.PendingHistoryUpload, 0, len(due))
	validEpisodeIDs := make([]string, 0, len(due))
	processed := 0
	var validationErr error
	for _, pending := range due {
		if s.store.HistoryDeleted(pending.SeriesID) {
			if err := s.store.AckHistoryUploads([]userstore.PendingHistoryUpload{pending}); err != nil {
				return 0, err
			}
			processed++
			continue
		}
		item := pending.HistoryItem
		episodeID := pending.EpisodeID
		if episodeID == "" {
			episodeID = s.resolveHistoryEpisodeID(item)
		}
		_, seriesErr := contentInt(item.SeriesID)
		_, episodeErr := contentInt(episodeID)
		if seriesErr != nil || episodeErr != nil {
			err := fmt.Errorf("历史 %s 缺少有效内容或分集 ID: %w", item.SeriesID, errParameterInvalid)
			s.markHistoryUploadFailed([]userstore.PendingHistoryUpload{pending}, err)
			validationErr = errors.Join(validationErr, err)
			continue
		}
		valid = append(valid, item)
		ack = append(ack, pending)
		validEpisodeIDs = append(validEpisodeIDs, episodeID)
	}
	if len(valid) == 0 {
		return processed, validationErr
	}
	uploaded, uploadErr := s.uploadPendingHistoryBatches(ctx, session, ack, valid, validEpisodeIDs)
	return processed + uploaded, errors.Join(validationErr, uploadErr)
}

// Parameter rejection is unambiguous, so split it to isolate bad records.
// Acknowledge each accepted sub-batch immediately; a failure in another branch
// must not replay accepted clocks. Transport/account errors stop this run.
func (s *Service) uploadPendingHistoryBatches(ctx context.Context, session syncSession, pending []userstore.PendingHistoryUpload, items []HistoryItem, episodeIDs []string) (int, error) {
	type batch struct{ start, end int }
	batches := []batch{{0, len(items)}}
	processed := 0
	var failures error
	for len(batches) > 0 {
		current := batches[0]
		batches = batches[1:]
		if !s.syncSessionCurrent(session) {
			return processed, errors.Join(failures, errSyncSessionChanged)
		}
		ack := pending[current.start:current.end]
		err := s.reportPlaybackProgressBatch(ctx, items[current.start:current.end], episodeIDs[current.start:current.end])
		if err != nil {
			if errors.Is(err, errParameterInvalid) && len(ack) > 1 {
				middle := current.start + len(ack)/2
				batches = append([]batch{{current.start, middle}, {middle, current.end}}, batches...)
				continue
			}
			s.markHistoryUploadFailed(ack, err)
			failures = errors.Join(failures, err)
			if !errors.Is(err, errParameterInvalid) {
				return processed, failures
			}
			continue
		}
		if !s.syncSessionCurrent(session) {
			return processed, errors.Join(failures, errSyncSessionChanged)
		}
		if err := s.store.AckHistoryUploads(ack); err != nil {
			s.markHistoryUploadFailed(ack, err)
			return processed, errors.Join(failures, err)
		}
		processed += len(ack)
		s.historyRetryMu.Lock()
		for _, item := range ack {
			delete(s.historyUploadRetries, historyUploadKey(item))
		}
		delete(s.historyScopeRetries, session.scope)
		s.historyRetryMu.Unlock()
	}
	return processed, failures
}

func (s *Service) processPendingHistoryDeletes() (int, error) {
	ctx := s.bindSyncSession(s.background.Context())
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return 0, nil
	}
	pending, err := s.store.PendingHistoryDeletes(50, session.scope)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	items := make([]HistoryItem, 0, len(pending))
	episodeIDs := make([]string, 0, len(pending))
	for _, queued := range pending {
		items = append(items, queued.HistoryItem)
		episodeIDs = append(episodeIDs, queued.EpisodeID)
	}
	send := make([]HistoryItem, 0, len(items))
	ack := make([]userstore.PendingHistoryDelete, 0, len(items))
	sendEpisodeIDs := make([]string, 0, len(items))
	for i, item := range items {
		retryItem := userstore.PendingHistoryUpload{HistoryItem: item, Scope: pending[i].Scope}
		if !s.historyUploadReady(retryItem) {
			continue
		}
		episodeID := pending[i].EpisodeID
		if episodeID == "" {
			episodeID = s.resolveHistoryEpisodeID(item)
		}
		if episodeID == "" {
			s.markHistoryUploadFailed([]userstore.PendingHistoryUpload{retryItem}, errParameterInvalid)
			continue
		}
		send = append(send, item)
		ack = append(ack, pending[i])
		sendEpisodeIDs = append(sendEpisodeIDs, episodeID)
	}
	if len(send) == 0 {
		return 0, nil
	}
	return s.deletePendingHistoryBatches(ctx, session, ack, send, sendEpisodeIDs)
}

func (s *Service) deletePendingHistoryBatches(ctx context.Context, session syncSession, pending []userstore.PendingHistoryDelete, items []HistoryItem, episodeIDs []string) (int, error) {
	type batch struct{ start, end int }
	batches := []batch{{0, len(items)}}
	processed := 0
	var failures error
	for len(batches) > 0 {
		current := batches[0]
		batches = batches[1:]
		if !s.syncSessionCurrent(session) {
			return processed, errors.Join(failures, errSyncSessionChanged)
		}
		ack := pending[current.start:current.end]
		requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.updateHistory(requestCtx, items[current.start:current.end], episodeIDs[current.start:current.end], true)
		cancel()
		if err != nil {
			if errors.Is(err, errParameterInvalid) && len(ack) > 1 {
				middle := current.start + len(ack)/2
				batches = append([]batch{{current.start, middle}, {middle, current.end}}, batches...)
				continue
			}
			failed := make([]userstore.PendingHistoryUpload, len(ack))
			for i, item := range ack {
				failed[i] = userstore.PendingHistoryUpload{HistoryItem: item.HistoryItem, Scope: item.Scope}
			}
			s.markHistoryUploadFailed(failed, err)
			failures = errors.Join(failures, err)
			if !errors.Is(err, errParameterInvalid) {
				return processed, failures
			}
			continue
		}
		if !s.syncSessionCurrent(session) {
			return processed, errors.Join(failures, errSyncSessionChanged)
		}
		if err := s.store.AckHistoryDeletes(ack); err != nil {
			failed := make([]userstore.PendingHistoryUpload, len(ack))
			for i, item := range ack {
				failed[i] = userstore.PendingHistoryUpload{HistoryItem: item.HistoryItem, Scope: item.Scope}
			}
			s.markHistoryUploadFailed(failed, err)
			return processed, errors.Join(failures, err)
		}
		processed += len(ack)
		s.historyRetryMu.Lock()
		for _, item := range ack {
			delete(s.historyUploadRetries, historyUploadKey(userstore.PendingHistoryUpload{HistoryItem: item.HistoryItem, Scope: item.Scope}))
		}
		delete(s.historyScopeRetries, session.scope)
		s.historyRetryMu.Unlock()
	}
	return processed, failures
}
func (s *Service) triggerPendingFavoriteSync() {
	s.background.Go(func(context.Context) {
		if _, err := s.syncPendingFavoriteOps(); err != nil {
			log.Printf("[favorites] pending sync failed: %v", err)
		}
	})
}

func (s *Service) syncPendingFavoriteOps() (int, error) {
	// Serialize upload workers, not local favorite changes. A slow network
	// must not block the local toggle; acknowledgments use queue revisions.
	s.favSyncMu.Lock()
	defer s.favSyncMu.Unlock()
	ctx := s.bindSyncSession(s.background.Context())
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return 0, nil
	}
	processed := 0
	for {
		items, err := s.store.PendingFavoriteOps(userSyncPageSize, session.scope)
		if err != nil || len(items) == 0 {
			if processed > 0 {
				s.touchFavoriteMutation()
			}
			return processed, err
		}
		for _, item := range items {
			requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.updateFavorite(requestCtx, item.SeriesID, item.Added)
			cancel()
			if err == nil {
				err = s.store.AckFavoriteOps([]userstore.PendingFavoriteOp{item})
			}
			if err != nil {
				if processed > 0 {
					s.touchFavoriteMutation()
				}
				return processed, err
			}
			processed++
		}
	}
}
