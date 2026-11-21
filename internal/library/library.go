package library

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
	"myguo/internal/mediautil"
	"myguo/internal/sanitize"
	userstore "myguo/internal/store"
)

func (s *Service) SaveProgress(ctx context.Context, seriesID, episodeID string, title, coverURL string, episodeNum, totalEpisodes int, positionSec, durationSec float64) error {
	session := s.currentSyncSession()
	return s.saveProgress(ctx, session, seriesID, episodeID, title, coverURL, episodeNum, totalEpisodes, positionSec, durationSec)
}

// PlaybackScope is an opaque identity for delayed local progress writes. It
// contains no credential. Store rechecks it under its transaction lock.
func (s *Service) PlaybackScope() string { return s.currentSyncSession().scope }

func (s *Service) SaveProgressForScope(ctx context.Context, scope, seriesID, episodeID, title, coverURL string, episodeNum, totalEpisodes int, positionSec, durationSec float64) error {
	session := s.currentSyncSession()
	if session.scope != scope {
		return errSyncSessionChanged
	}
	return s.saveProgress(ctx, session, seriesID, episodeID, title, coverURL, episodeNum, totalEpisodes, positionSec, durationSec)
}

func (s *Service) saveProgress(ctx context.Context, session syncSession, seriesID, episodeID, title, coverURL string, episodeNum, totalEpisodes int, positionSec, durationSec float64) error {
	seriesID = strings.TrimSpace(seriesID)
	episodeID = strings.TrimSpace(episodeID)
	if !mediautil.ValidContentID(seriesID) {
		return fmt.Errorf("历史 ID 无效")
	}
	s.historyMu.Lock()
	item := HistoryItem{
		SeriesID:      seriesID,
		Title:         title,
		CoverURL:      coverURL,
		EpisodeNum:    episodeNum,
		EpisodeTitle:  fmt.Sprintf("第%d集", episodeNum),
		PositionSec:   positionSec,
		DurationSec:   durationSec,
		TotalEpisodes: totalEpisodes,
		ContentType:   1004,
	}
	if existing, ok := s.store.GetHistoryItem(seriesID); ok && existing.ContentType != 0 {
		item.ContentType = existing.ContentType
	}
	if _, err := s.store.SavePlaybackProgressContext(ctx, item, episodeID, session.scope); err != nil {
		s.historyMu.Unlock()
		return err
	}
	s.cachedHistory = nil
	s.historyCachedAt = time.Time{}
	s.historyGeneration++

	s.historyMu.Unlock()

	s.touchHistoryMutation()
	s.triggerPendingHistorySync()
	return nil
}

func (s *Service) GetHistory(ctx context.Context) ([]HistoryItem, error) {
	statusErr := s.loggedInHistoryStatusError()
	s.historyMu.Lock()

	// 1. If in-memory cache is fresh (< 30s), return instantly
	if len(s.cachedHistory) > 0 && time.Since(s.historyCachedAt) < 30*time.Second {
		cached := make([]HistoryItem, len(s.cachedHistory))
		copy(cached, s.cachedHistory)
		s.historyMu.Unlock()
		return sanitize.Sanitize(cached), statusErr
	}

	// 2. Check local store (sub-millisecond instant return)
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

		// Stale-While-Revalidate: trigger async background sync without blocking user
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

	// 3. Local store is empty (e.g. first app launch with an existing account) -> sync synchronously
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

// RefreshHistory flushes local mutations, then reads the complete upstream
// list and returns the resulting authoritative snapshot to the caller.
func (s *Service) RefreshHistory(ctx context.Context) ([]HistoryItem, error) {
	if cookies, _ := s.credentials(); cookies == "" {
		return s.GetHistory(ctx)
	}
	generation := s.currentHistoryGeneration()
	if err := s.cleanupLegacyHistoryMutations(); err != nil {
		return nil, err
	}
	// Uploads already have a durable queue. Try them independently so a slow or
	// rejected write never consumes the read budget for other-device history.
	uploadDone := make(chan error, 1)
	s.background.Go(func(context.Context) {
		_, err := s.syncPendingHistoryMutations()
		uploadDone <- err
	})
	syncErr := s.syncRemoteHistory(ctx, generation)
	// Give the durable uploader a small, separate completion window. It never
	// shares the history request context or delays remote reads for a long retry.
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
	// Metadata enrichment and unavailable content are maintained in the
	// background. Neither makes a successfully read history list an error.
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

type remoteHistoryRow = hgapi.HistoryRow
type remoteHistoryPage = hgapi.HistoryPage

const (
	historyDetailBatchSize = 10
	historySyncTimeout     = 90 * time.Second
	historyMaxPages        = 2000
)

func (s *Service) fetchRemoteHistoryPage(ctx context.Context, offset int64) (*remoteHistoryPage, error) {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return nil, err
	}
	api := s.apiForSession(session)
	page, err := api.History(ctx, offset, "")
	if err != nil {
		return nil, err
	}
	return page, nil
}

func (s *Service) syncRemoteHistory(ctx context.Context, generation uint64) error {
	s.historyRemoteMu.Lock()
	defer s.historyRemoteMu.Unlock()

	ctx = s.bindSyncSession(ctx)
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	if err := s.cleanupLegacyHistoryMutations(); err != nil {
		return err
	}
	pendingIDs, err := s.store.PendingHistoryMutationIDs(session.scope)
	if err != nil {
		return err
	}
	if count, countErr := s.store.PendingHistoryMetadataCount(session.scope); countErr != nil {
		return countErr
	} else if count > 0 {
		s.scheduleHistoryMetadata(session)
	}
	ctx, cancel := context.WithTimeout(ctx, historySyncTimeout)
	defer cancel()

	var remoteRows []remoteHistoryRow
	snapshotComplete := false
	offset := int64(0)
	pagesRead := 0
	seenCursors := map[int64]bool{}
	var readErr error
	for page := 0; page < historyMaxPages; page++ {
		result, err := s.fetchRemoteHistoryPage(ctx, offset)
		if err != nil {
			readErr = fmt.Errorf("历史远端第%d页读取失败: %w", page+1, err)
			break
		}
		if result == nil {
			readErr = fmt.Errorf("历史远端第%d页没有响应", page+1)
			break
		}
		pagesRead++
		remoteRows = append(remoteRows, result.Items...)
		if !result.HasMore {
			snapshotComplete = true
			break
		}
		if len(result.Items) == 0 {
			readErr = fmt.Errorf("历史远端第%d页为空但仍标记有后续页", page+1)
			break
		}
		cursorDidNotAdvance := result.NextOffset <= offset
		if result.TimestampCursor {
			cursorDidNotAdvance = offset > 0 && result.NextOffset >= offset
		}
		if cursorDidNotAdvance || seenCursors[result.NextOffset] {
			readErr = fmt.Errorf("历史远端第%d页游标没有前进", page+1)
			break
		}
		seenCursors[result.NextOffset] = true
		offset = result.NextOffset
	}
	if !snapshotComplete && readErr == nil {
		readErr = fmt.Errorf("历史远端分页超过安全上限 %d", historyMaxPages)
	}

	// The local history model stores one row per series, while the remote
	// protocol can return several watched episodes for the same series.
	latestByID := make(map[string]remoteHistoryRow)
	for _, row := range remoteRows {
		if row.SeriesID == "" {
			continue
		}
		current, ok := latestByID[row.SeriesID]
		if !ok || row.LastWatchTime >= current.LastWatchTime {
			latestByID[row.SeriesID] = row
		}
	}
	var histBatch []HistoryItem
	remoteSnapshot := make([]HistoryItem, 0, len(latestByID))
	remoteEpisodeProgress := make(map[string]userstore.EpisodeProgress)
	for _, row := range remoteRows {
		epNum := row.VidIndex + 1
		if row.SeriesID == "" || epNum <= 0 {
			continue
		}
		updatedAt := row.LastWatchTime / 1000
		if updatedAt <= 0 {
			updatedAt = time.Now().Unix()
		}
		key := fmt.Sprintf("%s:%d", row.SeriesID, epNum)
		progress := userstore.EpisodeProgress{
			SeriesID: row.SeriesID, EpisodeNum: epNum,
			PositionSec: float64(row.CurrentPlayTime) / 1000,
			UpdatedAt:   updatedAt,
		}
		if previous, ok := remoteEpisodeProgress[key]; !ok || progress.UpdatedAt >= previous.UpdatedAt {
			remoteEpisodeProgress[key] = progress
		}
	}
	for _, it := range latestByID {
		id := it.SeriesID
		epNum := it.VidIndex + 1
		if epNum <= 0 {
			epNum = 1
		}
		posSec := float64(it.CurrentPlayTime) / 1000
		updatedAt := it.LastWatchTime / 1000
		if updatedAt <= 0 {
			updatedAt = time.Now().Unix()
		}
		exist, hasLocal := s.store.GetHistoryItem(id)
		current := HistoryItem{
			SeriesID:      id,
			Title:         "",
			CoverURL:      "",
			EpisodeNum:    epNum,
			EpisodeTitle:  fmt.Sprintf("第%d集", epNum),
			PositionSec:   posSec,
			TotalEpisodes: it.EpisodeCount,
			UpdatedAt:     updatedAt,
			ContentType:   1004,
		}
		if hasLocal {
			if !historyTitleNeedsEnrichment(exist.Title, exist.EpisodeNum) {
				current.Title = exist.Title
			}
			current.CoverURL = mediautil.NormalizeImageURL(exist.CoverURL)
			current.Intro = exist.Intro
			if exist.ContentType != 0 {
				current.ContentType = exist.ContentType
			}
			if current.TotalEpisodes <= 0 {
				current.TotalEpisodes = exist.TotalEpisodes
			}
			if exist.EpisodeNum == epNum {
				current.DurationSec = exist.DurationSec
			}
			if pendingIDs[id] || exist.UpdatedAt >= updatedAt {
				current.EpisodeNum = exist.EpisodeNum
				current.EpisodeTitle = exist.EpisodeTitle
				current.PositionSec = exist.PositionSec
				current.DurationSec = exist.DurationSec
				current.UpdatedAt = exist.UpdatedAt
			}
		}
		remoteSnapshot = append(remoteSnapshot, current)
		histBatch = append(histBatch, current)
	}
	if len(latestByID) == 0 && !snapshotComplete {
		return readErr
	}

	historySettled := s.historySnapshotSettled()
	s.historyMu.Lock()
	if s.historyGeneration != generation || !s.syncSessionCurrent(session) {
		s.historyMu.Unlock()
		return nil
	}
	// A snapshot can remove local-only rows only after every page is read and
	// the local write queue and recent playback clock have settled.
	authoritative := len(pendingIDs) == 0 && snapshotComplete && historySettled
	var applied bool
	var storeErr error
	if authoritative {
		applied, storeErr = s.store.ReplaceHistoryBatchIfCurrentScope(remoteSnapshot, session.scope)
	} else if len(histBatch) > 0 {
		applied, storeErr = s.store.SaveHistoryBatchIfCurrentScope(histBatch, session.scope)
	}
	if storeErr != nil {
		s.historyMu.Unlock()
		return storeErr
	}
	if !applied && (len(histBatch) > 0 || authoritative) {
		s.historyMu.Unlock()
		return errSyncSessionChanged
	}
	s.historyMu.Unlock()

	progressSnapshot := make([]userstore.EpisodeProgress, 0, len(remoteEpisodeProgress))
	for _, item := range remoteEpisodeProgress {
		progressSnapshot = append(progressSnapshot, item)
	}
	if authoritative {
		if _, err := s.store.ReplaceEpisodeProgressIfCurrentScope(progressSnapshot, session.scope); err != nil {
			return errors.Join(readErr, err)
		}
	} else if len(progressSnapshot) > 0 {
		if _, err := s.store.SaveEpisodeProgressBatchIfCurrentScope(progressSnapshot, session.scope); err != nil {
			return errors.Join(readErr, err)
		}
	}

	var metadataIDs []string
	metadataResolverAvailable := s.seriesBatch != nil || s.series != nil
	for _, item := range histBatch {
		coreMetadataMissing := historyTitleNeedsEnrichment(item.Title, item.EpisodeNum) || strings.TrimSpace(item.CoverURL) == "" || item.TotalEpisodes <= 0
		finalDurationMissing := item.TotalEpisodes > 0 && item.EpisodeNum == item.TotalEpisodes && item.DurationSec <= 0
		if ((metadataResolverAvailable && coreMetadataMissing) || finalDurationMissing) && !s.store.HistoryDeleted(item.SeriesID) {
			metadataIDs = append(metadataIDs, item.SeriesID)
		}
	}
	if err := s.store.QueueHistoryMetadata(metadataIDs, session.scope); err != nil {
		return errors.Join(readErr, err)
	}
	pendingMetadataCount, countErr := s.store.PendingHistoryMetadataCount(session.scope)
	if countErr != nil {
		return errors.Join(readErr, countErr)
	}
	if pendingMetadataCount > 0 {
		s.scheduleHistoryMetadata(session)
	}
	if len(remoteRows) == 0 || !snapshotComplete || pendingMetadataCount > 0 {
		log.Printf("[history] remote refresh pages=%d rows=%d series=%d merged=%d complete=%t metadata_pending=%d", pagesRead, len(remoteRows), len(latestByID), len(histBatch), snapshotComplete, pendingMetadataCount)
	}
	if _, err := s.historyFromStore(); err != nil {
		return errors.Join(readErr, err)
	}
	if len(histBatch) > 0 || authoritative {
		s.invalidateHistory()
	}
	return readErr
}

func historyMetadataNeedsEnrichment(item HistoryItem) bool {
	coreMetadataMissing := historyTitleNeedsEnrichment(item.Title, item.EpisodeNum) || strings.TrimSpace(item.CoverURL) == "" || item.TotalEpisodes <= 0
	finalDurationMissing := item.TotalEpisodes > 0 && item.EpisodeNum == item.TotalEpisodes && item.DurationSec <= 0
	return coreMetadataMissing || finalDurationMissing
}

func historyMetadataComplete(item HistoryItem) bool {
	coreMetadataComplete := !historyTitleNeedsEnrichment(item.Title, item.EpisodeNum) && strings.TrimSpace(item.CoverURL) != "" && item.TotalEpisodes > 0
	finalDurationComplete := item.EpisodeNum != item.TotalEpisodes || item.DurationSec > 0
	return coreMetadataComplete && finalDurationComplete
}

func (s *Service) scheduleHistoryMetadata(session syncSession) {
	s.historyMetadataMu.Lock()
	if s.historyMetadataSyncing {
		s.historyMetadataWake = true
		s.historyMetadataMu.Unlock()
		return
	}
	s.historyMetadataSyncing = true
	if s.historyMetadataTimer != nil {
		s.historyMetadataTimer.Stop()
		s.historyMetadataTimer = nil
	}
	s.historyMetadataMu.Unlock()
	if !s.background.Go(func(ctx context.Context) {
		defer func() {
			s.historyMetadataMu.Lock()
			s.historyMetadataSyncing = false
			wake := s.historyMetadataWake
			s.historyMetadataWake = false
			s.historyMetadataMu.Unlock()
			current := s.currentSyncSession()
			if wake && current.cookies != "" {
				s.scheduleHistoryMetadata(current)
			}
		}()
		for s.syncSessionCurrent(session) {
			if err := s.syncHistoryMetadata(ctx, session); err != nil {
				log.Printf("[history] metadata enrichment failed: %v", err)
			}
			next, pending, err := s.store.NextHistoryMetadataAttempt(session.scope)
			if err != nil {
				log.Printf("[history] metadata retry state read failed: %v", err)
				return
			}
			if pending {
				delay := time.Until(next)
				if delay < 0 {
					delay = time.Second
				}
				s.historyMetadataMu.Lock()
				s.historyMetadataTimer = time.AfterFunc(delay, func() {
					if s.syncSessionCurrent(session) {
						s.scheduleHistoryMetadata(session)
					}
				})
				s.historyMetadataMu.Unlock()
			}
			return
		}
	}) {
		s.historyMetadataMu.Lock()
		s.historyMetadataSyncing = false
		s.historyMetadataMu.Unlock()
	}
}

func (s *Service) syncHistoryMetadata(ctx context.Context, session syncSession) error {
	var failures []error
	for {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		pending, err := s.store.PendingHistoryMetadata(historyDetailBatchSize, session.scope, time.Now())
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if len(pending) == 0 {
			break
		}
		if !s.syncSessionCurrent(session) {
			return errors.Join(append(failures, errSyncSessionChanged)...)
		}

		ids := make([]string, len(pending))
		for i := range pending {
			ids[i] = pending[i].SeriesID
		}
		var details []model.SeriesDetail
		var requestErr error
		if s.seriesBatch != nil {
			requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			details, requestErr = s.seriesBatch(requestCtx, ids)
			cancel()
		} else {
			requestErr = errors.New("历史元数据批量接口不可用")
		}

		byID := make(map[string]model.SeriesDetail, len(details))
		for _, detail := range details {
			if detail.Series != nil && detail.Series.ID != "" {
				byID[detail.Series.ID] = detail
			}
		}
		var completed, retry []userstore.PendingHistoryMetadata
		for _, job := range pending {
			local, exists := s.store.GetHistoryItem(job.SeriesID)
			if !exists || s.store.HistoryDeleted(job.SeriesID) {
				completed = append(completed, job)
				continue
			}
			detail, exists := byID[job.SeriesID]
			if s.series != nil && (requestErr != nil || !exists || detail.Series == nil) {
				fallbackCtx, fallbackCancel := context.WithTimeout(ctx, 8*time.Second)
				series, fallbackErr := s.series(fallbackCtx, job.SeriesID)
				fallbackCancel()
				if fallbackErr == nil && series != nil {
					series.ID = job.SeriesID
					detail.Series = series
					detail.Episodes = nil
					exists = true
				} else if errors.Is(fallbackErr, hgapi.ErrContentUnavailable) {
					if err := s.store.MarkHistoryMetadataUnavailable(job, "该剧集已下架", time.Now().Add(7*24*time.Hour)); err != nil {
						failures = append(failures, err)
						retry = append(retry, job)
						continue
					}
					completed = append(completed, job)
					continue
				} else if fallbackErr != nil {
					failures = append(failures, fmt.Errorf("历史 %s 单剧详情补全失败: %w", job.SeriesID, fallbackErr))
				}
			}
			if (s.seriesBatch != nil || s.series != nil) && (!exists || detail.Series == nil) {
				retry = append(retry, job)
				continue
			}
			metadata := HistoryItem{
				SeriesID:   job.SeriesID,
				EpisodeNum: local.EpisodeNum,
			}
			if detail.Series != nil {
				metadata.Title = detail.Series.Title
				metadata.CoverURL = detail.Series.CoverURL
				metadata.Intro = detail.Series.Description
				metadata.TotalEpisodes = detail.Series.EpisodeCount
				metadata.ContentType = int(detail.Series.ContentType)
			}
			for _, episode := range detail.Episodes {
				if episode.Number == local.EpisodeNum {
					metadata.DurationSec = episode.Duration.Seconds()
					break
				}
			}
			if metadata.DurationSec <= 0 && (local.EpisodeNum == local.TotalEpisodes || local.DurationSec <= 0) {
				episodeCtx, episodeCancel := context.WithTimeout(ctx, 8*time.Second)
				episodes, episodeErr := s.episodes(episodeCtx, job.SeriesID)
				episodeCancel()
				if episodeErr == nil && episodes != nil {
					for _, episode := range episodes.Items {
						if episode.Number == local.EpisodeNum {
							metadata.DurationSec = episode.Duration.Seconds()
							break
						}
					}
				}
			}
			applied, err := s.store.EnrichHistoryMetadataIfCurrentScope(job.SeriesID, metadata, session.scope)
			if err != nil {
				failures = append(failures, err)
				retry = append(retry, job)
				continue
			}
			if !applied {
				if !s.syncSessionCurrent(session) {
					return errors.Join(append(failures, errSyncSessionChanged)...)
				}
				completed = append(completed, job)
				continue
			}
			local, exists = s.store.GetHistoryItem(job.SeriesID)
			if exists && historyMetadataComplete(local) {
				completed = append(completed, job)
			} else {
				retry = append(retry, job)
			}
		}
		if err := s.store.AckHistoryMetadata(completed); err != nil {
			failures = append(failures, err)
		}
		if len(retry) > 0 {
			s.retryHistoryMetadata(retry)
			failures = append(failures, fmt.Errorf("%d 条历史记录仍缺少剧名或封面", len(retry)))
		}
		if len(completed) > 0 {
			if _, err := s.historyFromStore(); err != nil {
				failures = append(failures, err)
			} else {
				s.invalidateHistory()
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) retryHistoryMetadata(pending []userstore.PendingHistoryMetadata) {
	maxAttempts := 0
	for _, item := range pending {
		maxAttempts = max(maxAttempts, item.Attempts+1)
	}
	delay := 30 * time.Second * time.Duration(1<<min(maxAttempts-1, 6))
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	if err := s.store.RetryHistoryMetadata(pending, time.Now().Add(delay)); err != nil {
		log.Printf("[history] metadata retry state save failed: %v", err)
	}
}

func historyTitleNeedsEnrichment(title string, episodeNum int) bool {
	title = strings.TrimSpace(title)
	if title == "" || title == "未知短剧" {
		return true
	}
	if !strings.HasPrefix(title, "第") || !strings.HasSuffix(title, "集") {
		return false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(title, "第"), "集")
	_, err := strconv.Atoi(number)
	return err == nil
}

func (s *Service) ToggleFavorite(ctx context.Context, seriesID, title, coverURL, desc string, totalEpisodes int) (bool, error) {
	ctx = s.bindSyncSession(ctx)
	session := ctx.Value(syncSessionContextKey{}).(syncSession)
	if !s.syncSessionCurrent(session) {
		return false, errSyncSessionChanged
	}
	seriesID = strings.TrimSpace(seriesID)
	if !mediautil.ValidContentID(seriesID) {
		return false, fmt.Errorf("收藏 ID 无效")
	}
	s.favMutationMu.Lock()
	defer s.favMutationMu.Unlock()
	if strings.TrimSpace(title) == "" {
		title = "未知短剧"
	}
	wasAdded, err := s.store.ToggleFavorite(FavoriteItem{
		SeriesID:      seriesID,
		Title:         title,
		CoverURL:      coverURL,
		Description:   desc,
		TotalEpisodes: totalEpisodes,
	}, session.scope)
	if err != nil {
		return false, err
	}
	s.favMu.Lock()
	s.cachedFavorites = nil
	s.favCachedAt = time.Time{}
	s.favGeneration++
	s.favMu.Unlock()
	s.touchFavoriteMutation()
	s.triggerPendingFavoriteSync()
	return wasAdded, nil
}

func (s *Service) IsFavorite(ctx context.Context, seriesID string) (bool, error) {
	return s.store.IsFavorite(seriesID), nil
}

func (s *Service) GetFavorites(ctx context.Context) ([]FavoriteItem, error) {
	s.favMu.Lock()

	// 1. If in-memory cache is fresh (< 30s), return instantly
	if len(s.cachedFavorites) > 0 && time.Since(s.favCachedAt) < 30*time.Second {
		cached := make([]FavoriteItem, len(s.cachedFavorites))
		copy(cached, s.cachedFavorites)
		s.favMu.Unlock()
		return sanitize.Sanitize(cached), nil
	}

	userCookies, _ := s.credentials()

	// 2. Check local store (sub-millisecond instant return)
	localItems, err := s.store.GetFavorites()
	if err != nil {
		s.favMu.Unlock()
		return nil, err
	}
	if len(localItems) > 0 {
		for i := range localItems {
			localItems[i].CoverURL = s.imageURL(localItems[i].CoverURL)
		}
		localItems = sanitize.Sanitize(localItems)
		s.cachedFavorites = localItems
		shouldSync := s.favCachedAt.IsZero() || time.Since(s.favCachedAt) >= 60*time.Second
		s.favCachedAt = time.Now()
		generation := s.favGeneration

		// Stale-While-Revalidate: trigger async background sync without blocking user
		if userCookies != "" && shouldSync && !s.favSyncing {
			s.favSyncing = true
			s.background.Go(func(ctx context.Context) {
				defer func() {
					s.favMu.Lock()
					s.favSyncing = false
					s.favMu.Unlock()
				}()
				if err := s.syncRemoteFavorites(ctx, generation); err != nil {
					log.Printf("[favorites] background sync failed: %v", err)
				}
			})
		}
		s.favMu.Unlock()
		return sanitize.Sanitize(localItems), nil
	}
	s.favMu.Unlock()

	if userCookies == "" {
		s.favMu.Lock()
		s.cachedFavorites = localItems
		s.favCachedAt = time.Now()
		s.favMu.Unlock()
		return sanitize.Sanitize(localItems), nil
	}

	// 3. Local store empty -> synchronous fetch
	if err := s.syncRemoteFavorites(ctx, s.currentFavoriteGeneration()); err != nil {
		return nil, err
	}

	s.favMu.Lock()
	defer s.favMu.Unlock()
	list, err := s.store.GetFavorites()
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].CoverURL = s.imageURL(list[i].CoverURL)
	}
	list = sanitize.Sanitize(list)
	s.cachedFavorites = list
	s.favCachedAt = time.Now()
	return sanitize.Sanitize(list), nil
}

// RefreshFavorites bypasses the stale-while-revalidate window after applying
// queued local mutations, so collection views can converge immediately.
func (s *Service) RefreshFavorites(ctx context.Context) ([]FavoriteItem, error) {
	if cookies, _ := s.credentials(); cookies == "" {
		return s.GetFavorites(ctx)
	}
	if err := s.syncFavoriteSnapshotWithRetry(ctx, s.currentFavoriteGeneration()); err != nil {
		if localItems, localErr := s.GetFavorites(ctx); localErr == nil {
			log.Printf("[favorites] refresh degraded to local snapshot: %v", err)
			return localItems, nil
		}
		return nil, err
	}
	return s.GetFavorites(ctx)
}

func (s *Service) syncRemoteFavorites(ctx context.Context, generation uint64) error {
	s.favRemoteMu.Lock()
	defer s.favRemoteMu.Unlock()

	ctx = s.bindSyncSession(ctx)
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	processed, err := s.syncPendingFavoriteOps()
	if err != nil {
		return fmt.Errorf("收藏待同步操作未完成: %w", err)
	}
	if processed > 0 {
		// The shelf list can briefly lag a successful mutation. Avoid replacing
		// local state with a stale snapshot; the next periodic sync will refresh.
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if !s.syncSessionCurrent(session) {
		return nil
	}
	api := s.apiForSession(session)
	shelfResp, err := api.Favorites(ctx)
	if err != nil {
		return err
	}

	shelf := *shelfResp.Data.VideoShelfInfo

	// The shelf response is authoritative only after every remote ID can be
	// represented locally. If metadata lookup is partial, merge what we have
	// and retry later rather than treating unresolved entries as deletions.
	remoteIDs := make(map[string]bool)
	seen := make(map[string]bool)
	for _, it := range shelf {
		if !mediautil.ValidContentID(it.SeriesID) {
			return fmt.Errorf("收藏书架包含无效内容 ID")
		}
		if it.SeriesID != "" && !seen[it.SeriesID] {
			seen[it.SeriesID] = true
			remoteIDs[it.SeriesID] = true
		}
	}
	if len(remoteIDs) == 0 {
		s.favMutationMu.Lock()
		defer s.favMutationMu.Unlock()
		if s.currentFavoriteGeneration() == generation && s.syncSessionCurrent(session) && s.favoriteSnapshotSettled() {
			if err := s.store.ReplaceFavoriteBatch(nil); err != nil {
				return err
			}
		}
		s.refreshFavoriteCache(generation)
		return nil
	}

	var missingIDs []string
	for id := range remoteIDs {
		if exist, ok := s.store.GetFavoriteItem(id); ok && exist.Title != "" && exist.CoverURL != "" {
			continue
		}
		missingIDs = append(missingIDs, id)
	}

	detailsMap := make(map[string]model.SeriesDetail)
	if len(missingIDs) > 0 {
		for i := 0; i < len(missingIDs); i += 100 {
			end := i + 100
			if end > len(missingIDs) {
				end = len(missingIDs)
			}
			bCtx, bCancel := context.WithTimeout(ctx, 8*time.Second)
			batch, bErr := s.client.GetSeriesBatch(bCtx, missingIDs[i:end])
			bCancel()
			if bErr == nil {
				for _, b := range batch {
					if b.Series != nil && b.Series.ID != "" {
						detailsMap[b.Series.ID] = b
					}
				}
			}
		}
	}

	var favBatch []FavoriteItem
	resolvedIDs := make(map[string]bool)
	for _, it := range shelf {
		if it.SeriesID == "" || resolvedIDs[it.SeriesID] {
			continue
		}
		modTime := time.Now().Unix()
		if it.ModifyTime > 0 {
			modTime = it.ModifyTime / 1000
		}

		if b, ok := detailsMap[it.SeriesID]; ok {
			favBatch = append(favBatch, FavoriteItem{
				SeriesID:      b.Series.ID,
				Title:         b.Series.Title,
				CoverURL:      mediautil.NormalizeImageURL(b.Series.CoverURL),
				Description:   b.Series.Description,
				TotalEpisodes: b.Series.EpisodeCount,
				AddedAt:       modTime,
			})
			resolvedIDs[it.SeriesID] = true
		} else if exist, ok := s.store.GetFavoriteItem(it.SeriesID); ok {
			favBatch = append(favBatch, FavoriteItem{
				SeriesID:      exist.SeriesID,
				Title:         exist.Title,
				CoverURL:      exist.CoverURL,
				Description:   exist.Description,
				TotalEpisodes: exist.TotalEpisodes,
				AddedAt:       modTime,
			})
			resolvedIDs[it.SeriesID] = true
		}
	}

	s.favMutationMu.Lock()
	defer s.favMutationMu.Unlock()
	if s.currentFavoriteGeneration() != generation || !s.syncSessionCurrent(session) {
		return nil
	}
	if len(favBatch) > 0 {
		if err := s.store.SaveFavoriteBatch(favBatch); err != nil {
			return err
		}
	}
	if len(resolvedIDs) != len(remoteIDs) || s.currentFavoriteGeneration() != generation || !s.favoriteSnapshotSettled() {
		return nil
	}
	if err := s.store.ReplaceFavoriteBatch(favBatch); err != nil {
		return err
	}
	s.refreshFavoriteCache(generation)
	return nil
}

func (s *Service) syncFavoriteSnapshotWithRetry(ctx context.Context, generation uint64) error {
	backoff := 250 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.syncRemoteFavorites(ctx, generation); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == 1 || errors.Is(lastErr, context.DeadlineExceeded) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return lastErr
}

func (s *Service) refreshFavoriteCache(generation uint64) {
	s.favMu.Lock()
	if s.favGeneration != generation {
		s.favMu.Unlock()
		return
	}
	list, err := s.store.GetFavorites()
	if err != nil {
		s.favMu.Unlock()
		return
	}
	for i := range list {
		list[i].CoverURL = s.imageURL(list[i].CoverURL)
	}
	s.cachedFavorites = list
	s.favCachedAt = time.Now()
	s.favMu.Unlock()
}
func (s *Service) ToggleLike(ctx context.Context, seriesID, episodeID, title, coverURL string, episodeNum int) (bool, error) {
	s.likeMutationMu.Lock()
	defer s.likeMutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ctx = s.bindSyncSession(ctx)
	session := ctx.Value(syncSessionContextKey{}).(syncSession)
	userCookies := session.cookies
	seriesID = strings.TrimSpace(seriesID)
	episodeID = strings.TrimSpace(episodeID)
	if !mediautil.ValidContentID(seriesID) || !mediautil.ValidContentID(episodeID) {
		return false, fmt.Errorf("点赞 ID 无效")
	}
	liked := !s.store.IsLiked(seriesID, episodeID, episodeNum)
	if userCookies != "" {
		if err := s.requestEpisodeLikeMutation(ctx, seriesID, episodeID, liked); err != nil {
			return false, err
		}
	}
	if !s.syncSessionCurrent(session) {
		return false, errSyncSessionChanged
	}
	s.likeMu.Lock()
	defer s.likeMu.Unlock()
	if strings.TrimSpace(title) == "" {
		title = "未知短剧"
	}
	err := s.store.SetLike(LikeItem{
		SeriesID:   seriesID,
		EpisodeID:  episodeID,
		Title:      title,
		CoverURL:   coverURL,
		EpisodeNum: episodeNum,
	}, liked, session.scope)
	if err == nil {
		s.cachedLikes = nil
		s.likeCachedAt = time.Time{}
		s.likeGeneration++
	}
	return liked, err
}

func (s *Service) GetLikes(ctx context.Context) ([]LikeItem, error) {
	userCookies, _ := s.credentials()
	s.likeMu.Lock()

	if len(s.cachedLikes) > 0 && time.Since(s.likeCachedAt) < 30*time.Second {
		cached := make([]LikeItem, len(s.cachedLikes))
		copy(cached, s.cachedLikes)
		s.likeMu.Unlock()
		return sanitize.Sanitize(cached), nil
	}

	localItems, err := s.store.GetLikes()
	if err != nil {
		s.likeMu.Unlock()
		return nil, err
	}
	if len(localItems) > 0 {
		for i := range localItems {
			localItems[i].CoverURL = s.imageURL(localItems[i].CoverURL)
		}
		localItems = sanitize.Sanitize(localItems)
		s.cachedLikes = localItems
		shouldSync := s.likeCachedAt.IsZero() || time.Since(s.likeCachedAt) >= 60*time.Second
		s.likeCachedAt = time.Now()
		generation := s.likeGeneration

		if userCookies != "" && shouldSync && !s.likeSyncing {
			s.likeSyncing = true
			s.background.Go(func(ctx context.Context) {
				defer func() {
					s.likeMu.Lock()
					s.likeSyncing = false
					s.likeMu.Unlock()
				}()
				if err := s.syncRemoteLikes(ctx, generation); err != nil {
					log.Printf("[likes] background sync failed: %v", err)
				}
			})
		}
		s.likeMu.Unlock()
		return sanitize.Sanitize(localItems), nil
	}
	s.likeMu.Unlock()

	if userCookies == "" {
		return []LikeItem{}, nil
	}
	if err := s.syncRemoteLikes(ctx, s.currentLikeGeneration()); err != nil {
		return nil, err
	}

	s.likeMu.Lock()
	defer s.likeMu.Unlock()
	list, err := s.store.GetLikes()
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].CoverURL = s.imageURL(list[i].CoverURL)
	}
	list = sanitize.Sanitize(list)
	s.cachedLikes = list
	s.likeCachedAt = time.Now()
	return sanitize.Sanitize(list), nil
}

func (s *Service) RefreshLikes(ctx context.Context) ([]LikeItem, error) {
	if cookies, _ := s.credentials(); cookies == "" {
		return s.GetLikes(ctx)
	}
	if err := s.syncRemoteLikes(ctx, s.currentLikeGeneration()); err != nil {
		return nil, err
	}
	return s.GetLikes(ctx)
}

func (s *Service) syncRemoteLikes(ctx context.Context, generation uint64) error {
	s.likeRemoteMu.Lock()
	defer s.likeRemoteMu.Unlock()

	ctx = s.bindSyncSession(ctx)
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	items, err := s.fetchAccountLikes(ctx)
	if err != nil {
		return err
	}
	s.likeMu.Lock()
	defer s.likeMu.Unlock()
	if s.likeGeneration != generation || !s.syncSessionCurrent(session) {
		return nil
	}
	if err := s.store.ReplaceLikeBatch(items); err != nil {
		return err
	}
	list, err := s.store.GetLikes()
	if err != nil {
		return err
	}
	for i := range list {
		list[i].CoverURL = s.imageURL(list[i].CoverURL)
	}
	s.cachedLikes = sanitize.Sanitize(list)
	s.likeCachedAt = time.Now()
	return nil
}

func (s *Service) fetchAccountLikes(ctx context.Context) ([]LikeItem, error) {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return nil, err
	}

	var results []LikeItem
	seen := make(map[string]bool)
	offset := 0
	for page := 0; page < 10; page++ {
		if !s.syncSessionCurrent(session) {
			return nil, errSyncSessionChanged
		}
		api := s.apiForSession(session)
		res, err := api.Likes(ctx, offset)
		if err != nil {
			return nil, err
		}

		for _, row := range res.Data.MixedDataList {
			video := row.VideoData
			seriesID := firstNonEmpty(row.SeriesID, video.SeriesID, row.BookID)
			episodeID := firstNonEmpty(video.Vid, row.SeriesID, row.BookID)
			title := firstNonEmpty(row.SeriesTitle, video.Detail.SeriesTitle, video.Title)
			cover := firstNonEmpty(row.SeriesCoverURL, video.CoverURL, video.Cover, video.ThumbURL)
			addedAt := video.DiggTime / 1000
			if addedAt <= 0 {
				addedAt = video.CreateTime / 1000
			}
			key := seriesID + "\x1f" + episodeID
			if seriesID == "" || episodeID == "" || title == "" || seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, LikeItem{
				SeriesID:      seriesID,
				EpisodeID:     episodeID,
				Title:         title,
				CoverURL:      mediautil.NormalizeImageURL(cover),
				EpisodeNum:    0,
				TotalEpisodes: video.EpisodeCnt,
				Description:   firstNonEmpty(row.SeriesIntro, video.Description),
				AddedAt:       addedAt,
			})
		}

		nextOffset := res.Data.NextOffset
		if nextOffset <= offset {
			nextOffset = offset + len(res.Data.MixedDataList)
		}
		if !(res.HasMore || res.Data.HasMore) {
			return results, nil
		}
		if len(res.Data.MixedDataList) == 0 || nextOffset <= offset {
			return nil, fmt.Errorf("点赞接口分页不完整")
		}
		offset = nextOffset
	}
	return nil, fmt.Errorf("点赞接口分页超过上限")
}

func (s *Service) requestEpisodeLikeMutation(ctx context.Context, seriesID, episodeID string, liked bool) error {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.apiForSession(session).SetEpisodeLike(ctx, seriesID, episodeID, liked)
}

func (s *Service) ToggleReservation(ctx context.Context, seriesID, title, coverURL, note, publishAt string) (bool, error) {
	ctx = s.bindSyncSession(ctx)
	session := ctx.Value(syncSessionContextKey{}).(syncSession)
	userCookies := session.cookies
	seriesID = strings.TrimSpace(seriesID)
	if !mediautil.ValidContentID(seriesID) {
		return false, fmt.Errorf("预约 ID 无效")
	}
	if userCookies == "" {
		return false, fmt.Errorf("请先登录后预约")
	}
	if strings.TrimSpace(title) == "" {
		title = "未知短剧"
	}

	// The reservation list is the source of truth; mutations are confirmed by
	// reading the account list back before returning.
	s.invalidateReservationCaches()
	s.resMu.Lock()
	s.cachedReservations = nil
	s.resCachedAt = time.Time{}
	s.resGeneration++
	generation := s.resGeneration
	s.resMu.Unlock()
	wasReserved := s.store.HasReservation(seriesID)
	opType := 1
	if wasReserved {
		opType = 2
	}
	if err := s.requestReservationMutation(ctx, seriesID, opType); err != nil {
		return false, err
	}
	if !s.syncSessionCurrent(session) {
		return false, errSyncSessionChanged
	}
	if !wasReserved {
		s.notify(ctx, Notification{
			Title:    "新剧预约成功",
			Body:     fmt.Sprintf("《%s》已加入预约列表，上线后将第一时间通知您！", title),
			SeriesID: seriesID,
		})
	}
	if _, err := s.syncRemoteReservations(ctx, generation); err != nil {
		// The write succeeded even when confirmation is slow or unavailable.
		return !wasReserved, nil
	}
	return !wasReserved, nil
}

func (s *Service) invalidateReservationCaches() {
	s.invalidateCaches("series_page:", "new_releases:")
}

func (s *Service) requestReservationMutation(ctx context.Context, seriesID string, opType int) error {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	return s.apiForSession(session).SetReservation(ctx, seriesID, opType)
}

func (s *Service) GetReservations(ctx context.Context) ([]ReservationItem, error) {
	s.resMu.Lock()

	// 1. If in-memory cache is fresh (< 60s), return instantly
	if len(s.cachedReservations) > 0 && time.Since(s.resCachedAt) < 60*time.Second {
		cached := make([]ReservationItem, len(s.cachedReservations))
		copy(cached, s.cachedReservations)
		s.resMu.Unlock()
		return sanitize.Sanitize(cached), nil
	}

	// 2. Check local store (sub-millisecond instant return)
	storeList, err := s.store.GetReservations()
	if err != nil {
		s.resMu.Unlock()
		return nil, err
	}
	for i := range storeList {
		storeList[i].CoverURL = s.imageURL(storeList[i].CoverURL)
	}
	storeList = sanitize.Sanitize(storeList)

	userCookies, _ := s.credentials()
	if userCookies == "" {
		s.cachedReservations = storeList
		s.resCachedAt = time.Now()
		s.resMu.Unlock()
		return sanitize.Sanitize(storeList), nil
	}

	if len(storeList) > 0 {
		s.cachedReservations = storeList
		shouldSync := s.resCachedAt.IsZero() || time.Since(s.resCachedAt) >= 60*time.Second
		s.resCachedAt = time.Now()
		generation := s.resGeneration
		// Stale-While-Revalidate: trigger async background sync without blocking user
		if shouldSync && !s.resSyncing {
			s.resSyncing = true
			s.background.Go(func(ctx context.Context) {
				defer func() {
					s.resMu.Lock()
					s.resSyncing = false
					s.resMu.Unlock()
				}()
				if _, err := s.syncRemoteReservations(ctx, generation); err != nil {
					log.Printf("[reservations] background sync failed: %v", err)
				}
			})
		}
		s.resMu.Unlock()
		return sanitize.Sanitize(storeList), nil
	}
	s.resMu.Unlock()

	// 3. Local store empty -> synchronous fetch
	results, err := s.syncRemoteReservations(ctx, s.currentReservationGeneration())
	return sanitize.Sanitize(results), err
}

func (s *Service) RefreshReservations(ctx context.Context) ([]ReservationItem, error) {
	if cookies, _ := s.credentials(); cookies == "" {
		return s.GetReservations(ctx)
	}
	list, err := s.syncRemoteReservations(ctx, s.currentReservationGeneration())
	if err != nil {
		return nil, err
	}
	if list == nil {
		return s.GetReservations(ctx)
	}
	return list, nil
}

func (s *Service) syncRemoteReservations(ctx context.Context, generation uint64) ([]ReservationItem, error) {
	s.resRemoteMu.Lock()
	defer s.resRemoteMu.Unlock()

	ctx = s.bindSyncSession(ctx)
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	var (
		online  []ReservationItem
		offline []ReservationItem
		onErr   error
		offErr  error
		wg      sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		online, onErr = s.fetchAccountReservations(ctx, true)
	}()
	go func() {
		defer wg.Done()
		offline, offErr = s.fetchAccountReservations(ctx, false)
	}()
	wg.Wait()

	if s.currentReservationGeneration() != generation || !s.syncSessionCurrent(session) {
		return nil, nil
	}
	if onErr != nil {
		return nil, onErr
	}
	if offErr != nil {
		return nil, offErr
	}

	prevReservations, prevErr := s.store.GetReservations()
	if prevErr != nil {
		return nil, prevErr
	}
	prevOnline := make(map[string]bool)
	for _, r := range prevReservations {
		prevOnline[r.SeriesID] = r.IsOnline
	}

	results := append(online, offline...)
	s.resMu.Lock()
	if s.resGeneration != generation || !s.syncSessionCurrent(session) {
		s.resMu.Unlock()
		return nil, nil
	}
	if err := s.store.ReplaceReservationBatch(results); err != nil {
		s.resMu.Unlock()
		return nil, err
	}
	storeList, storeErr := s.store.GetReservations()
	if storeErr != nil {
		s.resMu.Unlock()
		return nil, storeErr
	}
	for i := range storeList {
		storeList[i].CoverURL = s.imageURL(storeList[i].CoverURL)
	}
	storeList = sanitize.Sanitize(storeList)
	s.cachedReservations = storeList
	s.resCachedAt = time.Now()
	s.resMu.Unlock()

	// Announce only a known pending reservation becoming online, after the
	// snapshot is committed. An older released row first synced from another
	// device is not a release event, and failed writes must not repeat alerts.
	for _, it := range online {
		wasOnline, known := prevOnline[it.SeriesID]
		if !known || wasOnline || !it.IsOnline {
			continue
		}
		if s.currentReservationGeneration() != generation || !s.syncSessionCurrent(session) {
			break
		}
		s.notify(ctx, Notification{
			Title:    "新剧开播提醒",
			Body:     fmt.Sprintf("您预约的《%s》已正式上线，点击立即观看！", it.Title),
			SeriesID: it.SeriesID,
		})
	}

	return sanitize.Sanitize(storeList), nil
}

func (s *Service) fetchAccountReservations(ctx context.Context, online bool) ([]ReservationItem, error) {
	session, err := s.sessionForRequest(ctx)
	if err != nil {
		return nil, err
	}
	var results []ReservationItem
	offset := 0

	for {
		if !s.syncSessionCurrent(session) {
			return nil, errSyncSessionChanged
		}
		api := s.apiForSession(session)
		res, err := api.Reservations(ctx, offset, online)
		if err != nil {
			return nil, err
		}

		for _, it := range res.Data.Items {
			id := it.ItemID
			title := it.Name
			cover := it.Cover
			if it.Row != nil {
				// Reservation item_id is the scheduled notification, while
				// subscribe_data.series_id is the playable series after release.
				if it.Row.IDStr != "" {
					id = it.Row.IDStr
				} else if it.Row.ID != "" {
					id = it.Row.ID
				}
				if it.Row.Title != "" {
					title = it.Row.Title
				}
				if it.Row.Cover != "" {
					cover = it.Row.Cover
				}
			}
			if id == "" || id == "0" {
				continue
			}

			note := it.SubInfo
			if note == "" {
				note = it.Expected
			}
			if it.IsOnline {
				if note == "" {
					note = "已上线"
				}
			} else if note == "" {
				note = "预约中"
			}

			publishTime := it.ScheduleTime
			if publishTime == 0 {
				publishTime = it.PlannedTime
			}
			var publishAt string
			if publishTime > 0 {
				publishAt = time.Unix(publishTime, 0).Format(time.RFC3339)
			}

			results = append(results, ReservationItem{
				SeriesID:  id,
				Title:     title,
				CoverURL:  s.imageURL(cover),
				Note:      note,
				PublishAt: publishAt,
				IsOnline:  it.IsOnline,
				AddedAt:   time.Now().Unix(),
			})
		}

		if !res.Data.HasMore {
			return results, nil
		}
		if len(res.Data.Items) == 0 || res.Data.NextOffset <= offset {
			return nil, fmt.Errorf("预约接口分页不完整")
		}
		offset = res.Data.NextOffset
	}
}
