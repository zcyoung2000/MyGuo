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

func (s *Service) requestHistoryDelete(ctx context.Context, items []HistoryItem, episodeIDs []string) error {
	if len(items) == 0 || len(items) != len(episodeIDs) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if len(items) > 1 {
		err := s.updateHistory(ctx, items, episodeIDs, true)
		if err == nil || !errors.Is(err, errParameterInvalid) {
			return err
		}
		// Split only after the server explicitly rejects the parameters,
		// isolating a batch-size limit without acknowledging a partial page.
		middle := len(items) / 2
		if err := s.requestHistoryDelete(ctx, items[:middle], episodeIDs[:middle]); err != nil {
			return err
		}
		return s.requestHistoryDelete(ctx, items[middle:], episodeIDs[middle:])
	}
	return s.updateHistory(ctx, items, episodeIDs, true)
}

func (s *Service) reportPlaybackProgress(ctx context.Context, items []HistoryItem, episodeIDs []string) error {
	if len(items) == 0 || len(items) != len(episodeIDs) {
		return nil
	}
	if len(items) > 1 {
		if err := s.reportPlaybackProgressBatch(ctx, items, episodeIDs); err == nil {
			return nil
		} else if !errors.Is(err, errParameterInvalid) {
			return err
		}
		// Split only after the server explicitly rejects the parameters,
		// isolating a batch-size limit without acknowledging a partial page.
		middle := len(items) / 2
		if err := s.reportPlaybackProgress(ctx, items[:middle], episodeIDs[:middle]); err != nil {
			return err
		}
		return s.reportPlaybackProgress(ctx, items[middle:], episodeIDs[middle:])
	}
	return s.reportPlaybackProgressBatch(ctx, items, episodeIDs)
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

func historyUploadKey(item userstore.PendingHistoryUpload) string {
	// New local clocks replace the durable revision every few seconds. They
	// must not bypass an upstream failure's cooldown for this account/series.
	return item.Scope + "\x00" + item.SeriesID
}

func (s *Service) markHistoryUploadFailed(items []userstore.PendingHistoryUpload, err error) {
	if s.historyUploadRetries == nil {
		s.historyUploadRetries = make(map[string]historyUploadRetry)
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

func (s *Service) historyUploadReady(item userstore.PendingHistoryUpload) bool {
	retry, ok := s.historyUploadRetries[historyUploadKey(item)]
	return !ok || !time.Now().Before(retry.nextAt)
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
	for _, pending := range due {
		if s.store.HistoryDeleted(pending.SeriesID) {
			if err := s.store.AckHistoryUploads([]userstore.PendingHistoryUpload{pending}); err != nil {
				return 0, err
			}
			continue
		}
		item := pending.HistoryItem
		episodeID := pending.EpisodeID
		if episodeID == "" {
			episodeID = s.resolveHistoryEpisodeID(item)
		}
		if _, err := contentInt(item.SeriesID); err != nil {
			return 0, err
		}
		if _, err := contentInt(episodeID); err != nil {
			return 0, fmt.Errorf("历史 %s 缺少有效分集 ID", item.SeriesID)
		}
		valid = append(valid, item)
		ack = append(ack, pending)
		validEpisodeIDs = append(validEpisodeIDs, episodeID)
	}
	if len(valid) == 0 {
		return 0, nil
	}
	if err := s.reportPlaybackProgress(ctx, valid, validEpisodeIDs); err != nil {
		s.markHistoryUploadFailed(ack, err)
		return 0, err
	}
	if !s.syncSessionCurrent(session) {
		return 0, errSyncSessionChanged
	}
	acknowledged := len(ack)
	if s.historyUploadRetries == nil {
		s.historyUploadRetries = make(map[string]historyUploadRetry)
	}
	for _, item := range ack {
		delete(s.historyUploadRetries, historyUploadKey(item))
	}
	return acknowledged, s.store.AckHistoryUploads(ack)
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
		episodeID := pending[i].EpisodeID
		if episodeID == "" {
			episodeID = s.resolveHistoryEpisodeID(item)
		}
		if episodeID == "" {
			continue
		}
		send = append(send, item)
		ack = append(ack, pending[i])
		sendEpisodeIDs = append(sendEpisodeIDs, episodeID)
	}
	if len(send) == 0 {
		return 0, fmt.Errorf("待删除历史缺少可解析分集")
	}
	if err := s.requestHistoryDelete(ctx, send, sendEpisodeIDs); err != nil {
		return 0, err
	}
	acknowledged := len(ack)
	return acknowledged, s.store.AckHistoryDeletes(ack)
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
