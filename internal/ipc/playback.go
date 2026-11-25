package ipc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
	"myguo/internal/media"
	"myguo/internal/sparedevice"
)

var ErrNoPlayableEpisodes = errors.New("该剧暂未提供可播放剧集")
var ErrPlaybackAccessLimited = errors.New("播放信息访问受限，请稍后重试")

func (s *HongguoService) UpdateFeedPreloadWindow(ctx context.Context, version int64, currentEpisodeID, currentQuality string, items []media.FeedPreloadItem) ([]media.FeedPreloadStatus, error) {
	statuses := s.media.SetFeedPreloadWindow(version, currentEpisodeID, currentQuality, items)
	// Windows has no published buffered ranges and its Media Foundation player
	// cannot pre-decode a neighbor. The only way to make auto-advance start
	// instantly is to have the complete MP4 ready, because PlayEpisode only
	// treats a finished local file as IsFullCached and switches to StreamURL;
	// an HLS-only prefix still forces a 6*target-duration wait on promotion.
	// The native UI never calls WarmupFeed (that path lives in the WebView
	// frontend), so nothing else drives the full-file prefetch window here.
	// Mirror the HLS window into the full-file prefetch scheduler so the next
	// episode is downloaded while the current one plays. macOS keeps its
	// original behavior and is untouched.
	if runtime.GOOS == "windows" {
		s.updateFullPrefetchWindow(currentEpisodeID, currentQuality, items)
	}
	return statuses, nil
}

// updateFullPrefetchWindow mirrors the renderer's lookahead into the complete
// file prefetch scheduler. It is Windows-only; callers guard with GOOS.
func (s *HongguoService) updateFullPrefetchWindow(currentEpisodeID, currentQuality string, items []media.FeedPreloadItem) {
	// A transient empty lookahead must not replace the window. It happens on
	// every auto-advance: the promoted episode is not Ready yet, so admission
	// is hard-blocked and the renderer submits no neighbor item. Replacing an
	// empty set would call execution.cancel() on the in-flight complete-MP4
	// download of the episode we are switching into, so it never finishes and
	// promotion is stuck on HLS forever. Keep the previous window instead; a
	// later tick with resolved neighbors republishes it normally. An empty
	// current episode is an explicit reset/dispose and still clears the window.
	if currentEpisodeID != "" {
		hasNeighbor := false
		for _, item := range items {
			if item.EpisodeID != "" && item.EpisodeID != currentEpisodeID {
				hasNeighbor = true
				break
			}
		}
		if !hasNeighbor {
			return
		}
	}
	// Build a stable signature of the desired window. ReplacePrefetchWindow
	// cancels every active task not in its snapshot, so calling it on every
	// 500ms renderer tick would restart the full download before it finishes.
	// Only publish when the set of episodes actually changes.
	var sb strings.Builder
	sb.WriteString(currentEpisodeID)
	sb.WriteByte('|')
	sb.WriteString(currentQuality)
	for i := 0; i < len(items) && i < 2; i++ {
		sb.WriteByte('|')
		sb.WriteString(items[i].EpisodeID)
		sb.WriteByte('/')
		sb.WriteString(items[i].Quality)
	}
	signature := sb.String()
	s.fullPrefetchMu.Lock()
	defer s.fullPrefetchMu.Unlock()
	if signature == s.fullPrefetchSignature {
		return
	}
	s.fullPrefetchSignature = signature

	tasks := make([]media.PrefetchTask, 0, len(items)+1)
	seen := make(map[string]bool, len(items)+1)
	add := func(id, seriesID, quality string, number int, protect bool) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		tasks = append(tasks, media.PrefetchTask{
			SeriesID:  seriesID,
			EpisodeID: id,
			Number:    number,
			Quality:   quality,
			// Keep the playing episode protected without downloading a
			// duplicate full file; the renderer already owns its stream.
			ProtectOnly: protect,
			Priority:    len(tasks),
		})
	}
	add(currentEpisodeID, "", currentQuality, 0, true)
	for i, item := range items {
		if i >= 2 {
			break
		}
		add(item.EpisodeID, item.SeriesID, item.Quality, item.Number, false)
	}
	s.media.ReplacePrefetchWindow(tasks)
}

// RefreshCachedEpisode upgrades a neighbor DTO after its complete MP4 prefetch
// finishes. Promotion runs on the controller loop, so this must stay local and
// must not call PlayEpisode (which can fetch episode metadata from the network).
// Windows only; every other case returns the original DTO untouched.
func (s *HongguoService) RefreshCachedEpisode(_ context.Context, dto *PlaybackDTO) (*PlaybackDTO, error) {
	if runtime.GOOS != "windows" || dto == nil || dto.IsFullCached || dto.EpisodeID == "" {
		return dto, nil
	}
	if s.media == nil {
		return dto, nil
	}
	path := media.EpisodeCachePath(s.media.CacheDir(), dto.EpisodeID, dto.Quality)
	if path == "" || !media.FileExists(path) {
		return dto, nil
	}
	refreshed := *dto
	refreshed.IsFullCached = true
	if refreshed.FullStreamURL == "" {
		refreshed.FullStreamURL = s.media.VideoURL(dto.EpisodeID, dto.SeriesID, dto.EpisodeNum, dto.Quality)
	}
	refreshed.StreamURL = refreshed.FullStreamURL
	return &refreshed, nil
}

func (s *HongguoService) PlayEpisode(ctx context.Context, seriesID, episodeID string, episodeNum int, quality string, preferHLS bool) (result *PlaybackDTO, playbackErr error) {
	started := time.Now()
	phase := "resolve-episode"
	if episodeNum <= 0 {
		episodeNum = 1
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	defer func() {
		log.Printf("[feed-playback] series=%q episode=%q phase=%s elapsedMs=%d cancelled=%t", seriesID, episodeID, phase, time.Since(started).Milliseconds(), ctx.Err() != nil)
		if playbackErr != nil {
			log.Printf("[feed-playback] series=%q phase=%s error=%q", seriesID, phase, playbackErr)
			if errors.Is(playbackErr, ErrNoPlayableEpisodes) || errors.Is(playbackErr, hgapi.ErrContentUnavailable) {
				if err := s.store.RemoveUnavailableFeedSeries(seriesID); err != nil {
					log.Printf("[feed-catalog] remove unavailable series=%q: %v", seriesID, err)
				}
			}
		}
	}()

	var episodePosterURL string
	var commentCount int64
	var playbackDurationSec int
	cachedStart := false
	var eps *hgapi.Page[model.Episode]
	if episodeID == "" {
		// A full local cache can start instantly without a quality probe or
		// another episode-list round trip. Series details/comments still load
		// in the background on the frontend.
		candidates, err := s.store.GetCachedEpisodesAt(seriesID, normalizeVideoQuality(quality), episodeNum)
		if err != nil {
			return nil, err
		}
		for _, cached := range candidates {
			if !s.media.HasCachedEpisode(cached.EpisodeID, cached.Quality) {
				continue
			}
			episodeID = cached.EpisodeID
			episodeNum = cached.EpisodeNum
			quality = cached.Quality
			cachedStart = true
			break
		}
	}

	if episodeID == "" && !cachedStart {
		var err error
		eps, err = s.listEpisodes(ctx, seriesID)
		if err != nil {
			if errors.Is(err, hgapi.ErrAnonymousLimited) {
				return nil, fmt.Errorf("分集访问受限，请稍后重试: %w", err)
			}
			return nil, fmt.Errorf("获取分集失败: %w", err)
		}
		if len(pageItems(eps)) == 0 {
			return nil, ErrNoPlayableEpisodes
		}
		targetNum := episodeNum
		found := false
		var selected model.Episode
		for _, ep := range pageItems(eps) {
			if ep.Number == targetNum && ep.Playable && ep.ID != "" {
				episodeID = ep.ID
				selected = ep
				playbackDurationSec = int(selected.Duration.Seconds())
				commentCount = ep.CommentCount
				found = true
				break
			}
		}
		if !found {
			for _, ep := range pageItems(eps) {
				if ep.Playable && ep.ID != "" {
					episodeID, episodeNum, selected, commentCount = ep.ID, ep.Number, ep, ep.CommentCount
					break
				}
			}
			if episodeID == "" {
				return nil, ErrNoPlayableEpisodes
			}
		}
		playbackDurationSec = int(selected.Duration.Seconds())
		episodePosterURL = s.media.ImageURL(selected.CoverURL)
	}

	if eps == nil && s.client != nil && !cachedStart {
		pageCtx, pageCancel := context.WithTimeout(context.WithoutCancel(ctx), seriesDetailTimeout)
		defer pageCancel()
		if page, err := s.listEpisodes(pageCtx, seriesID); err == nil {
			eps = page
		}
	}
	s.media.PrefetchNextPlaybackModels(media.NextEpisodes(pageItems(eps), episodeNum))

	if episodeID == "" {
		return nil, fmt.Errorf("failed to resolve episode ID for %s", seriesID)
	}
	cachedStart = cachedStart || s.media.HasCachedEpisode(episodeID, quality)

	var qualityInfo QualitySelection
	resolvedAt := time.Now()
	log.Printf("[feed-playback] series=%q phase=episode-resolved elapsedMs=%d cached=%t", seriesID, resolvedAt.Sub(started).Milliseconds(), cachedStart)
	phase = "playback-info"
	if cachedStart {
		qualityInfo = QualitySelection{
			Options:     fallbackQualityOptions(quality),
			Selected:    quality,
			DurationSec: playbackDurationSec,
		}
	} else {
		playbackSession := sparedevice.RandomSession()
		defer s.media.ReleasePlaybackSession(playbackSession)
		info, err := s.media.PlaybackQualitiesForSession(ctx, episodeID, quality, playbackSession)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Negotiation already tried protocol recovery and the spare pool. HLS
			// and full-file transport use this same model resolver; returning
			// their URLs cannot repair denied access and repeats the requests.
			if !s.media.HasCachedHLS(episodeID, quality) {
				if errors.Is(err, hgapi.ErrAnonymousLimited) || errors.Is(err, hgapi.ErrRateLimited) {
					return nil, fmt.Errorf("%w: %w", ErrPlaybackAccessLimited, err)
				}
				return nil, fmt.Errorf("获取播放信息失败: %w", err)
			}
			// A completed local HLS copy can play without a new model.
			qualityInfo = QualitySelection{
				Options:     fallbackQualityOptions(normalizeVideoQuality(quality)),
				Selected:    normalizeVideoQuality(quality),
				DurationSec: playbackDurationSec,
			}
		} else {
			qualityInfo = info
		}
	}
	_ = s.store.RememberEpisode(seriesID, episodeID, episodeNum, qualityInfo.Selected)
	log.Printf("[feed-playback] series=%q phase=quality-resolved elapsedMs=%d phaseMs=%d", seriesID, time.Since(started).Milliseconds(), time.Since(resolvedAt).Milliseconds())
	phase = "local-state"
	isFullCached := fileExists(episodeCachePath(s.media.CacheDir(), episodeID, qualityInfo.Selected))
	fullStreamURL := s.media.VideoURL(episodeID, seriesID, episodeNum, qualityInfo.Selected)
	streamURL := fullStreamURL
	progressiveStreamURL := ""
	if !isFullCached {
		streamURL = s.media.ProgressiveVideoURL(episodeID, seriesID, episodeNum, qualityInfo.Selected)
		progressiveStreamURL = streamURL
	}
	hlsStreamURL := ""
	isHLSCached := s.media.HasCachedHLS(episodeID, qualityInfo.Selected)
	if !isFullCached {
		// Negotiation must not start a media job: a late IPC response could
		// otherwise restart a preload after the scheduler withdrew admission.
		// Return the alternative even for metadata-only lookahead so admission
		// can select HLS without repeating SDK negotiation or another IPC call.
		hlsStreamURL = s.media.HLSPlaylistURL(episodeID, seriesID, episodeNum, qualityInfo.Selected)
		if preferHLS || isHLSCached {
			streamURL = hlsStreamURL
		}
	}

	hist, err := s.store.GetHistory()
	if err != nil {
		return nil, err
	}
	var lastPos float64
	for _, h := range hist {
		if h.SeriesID == seriesID && h.EpisodeNum == episodeNum {
			lastPos = h.PositionSec
			break
		}
	}

	isLiked := s.store.IsLiked(seriesID, episodeID, episodeNum)
	phase = "complete"

	return sanitizeIPC(&PlaybackDTO{
		SeriesID:             seriesID,
		EpisodeID:            episodeID,
		EpisodeNum:           episodeNum,
		Title:                fmt.Sprintf("第%d集", episodeNum),
		StreamURL:            streamURL,
		FullStreamURL:        fullStreamURL,
		ProgressiveStreamURL: progressiveStreamURL,
		HLSStreamURL:         hlsStreamURL,
		PosterURL:            episodePosterURL,
		DurationSec:          firstPositiveInt(qualityInfo.DurationSec, playbackDurationSec),
		LastPosition:         lastPos,
		IsLiked:              isLiked,
		CommentCount:         commentCount,
		Quality:              qualityInfo.Selected,
		Qualities:            qualityInfo.Options,
		IsFullCached:         isFullCached,
		IsHLSCached:          isHLSCached,
	}), nil
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func normalizeNetworkProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "weak", "medium", "strong":
		return strings.ToLower(strings.TrimSpace(profile))
	default:
		return "unknown"
	}
}

func truncateString(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func warmupWindowLimits(profile string) (next, previous int) {
	switch normalizeNetworkProfile(profile) {
	case "weak":
		// Preserve the immediate next progressive stream, but defer all full-file
		// work until playback proves that the connection can sustain it.
		return 1, 0
	case "medium":
		return 2, 1
	case "strong":
		return 3, 2
	default:
		return 2, 1
	}
}

func fallbackQualityOptions(selected string) []QualityOptionDTO {
	selected = normalizeVideoQuality(selected)
	options := make([]QualityOptionDTO, 0, len(videoQualities()))
	for _, quality := range videoQualities() {
		options = append(options, QualityOptionDTO{
			ID:        quality,
			Label:     qualityLabel(quality),
			Available: true,
			Active:    quality == selected,
		})
	}
	return options
}

// WarmupFeed performs bidirectional background preheating for adjacent dramas in the recommend feed.
func (s *HongguoService) WarmupFeed(ctx context.Context, centerSeriesID, centerEpisodeID string, prevSeriesIDs []string, nextSeriesIDs []string, quality, networkProfile string) error {
	quality = normalizeVideoQuality(quality)
	networkProfile = normalizeNetworkProfile(networkProfile)
	s.feedWarmupMu.Lock()
	s.feedWarmupGeneration++
	warmupGeneration := s.feedWarmupGeneration
	s.feedWarmupMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()

		type warmupItem struct {
			id           string
			priority     int
			deferred     bool
			prefetchOnly bool
		}
		items := make([]warmupItem, 0, 6)
		seen := make(map[string]bool, 6)
		appendSeries := func(id string, priority int, deferred, prefetchOnly bool) {
			if id != "" && !seen[id] {
				seen[id] = true
				items = append(items, warmupItem{id: id, priority: priority, deferred: deferred, prefetchOnly: prefetchOnly})
			}
		}
		nextLimit, previousLimit := warmupWindowLimits(networkProfile)
		appendSeries(centerSeriesID, 0, false, false)
		for i, id := range nextSeriesIDs {
			if i >= nextLimit {
				break
			}
			// The immediate next may fall back to a full file if its renderer
			// stream cannot start. Farther items are HLS-prefetch only so they
			// never compete with the handoff path.
			appendSeries(id, i+1, true, i > 0)
		}
		for i, id := range prevSeriesIDs {
			if i >= previousLimit {
				break
			}
			appendSeries(id, len(nextSeriesIDs)+i+1, true, true)
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].priority < items[j].priority })

		var tasksMu sync.Mutex
		tasks := make([]PrefetchTask, 0, len(items))
		publish := func(task PrefetchTask) {
			tasksMu.Lock()
			tasks = append(tasks, task)
			snapshot := make([]PrefetchTask, len(tasks))
			copy(snapshot, tasks)
			tasksMu.Unlock()

			// Serialize snapshots through the warmup lock. Otherwise a stale,
			// smaller snapshot could replace the window after a larger one and
			// cancel work that was just admitted.
			s.feedWarmupMu.Lock()
			defer s.feedWarmupMu.Unlock()
			if warmupGeneration != s.feedWarmupGeneration {
				return
			}
			s.media.ReplacePrefetchWindow(snapshot)
		}
		resolveItem := func(item warmupItem) {
			seriesID := item.id
			episodes, err := s.listEpisodes(ctx, seriesID)
			if err != nil || len(pageItems(episodes)) == 0 {
				return
			}
			episode := pageItems(episodes)[0]
			_ = s.store.RememberEpisode(seriesID, episode.ID, episode.Number, quality)
			publish(PrefetchTask{
				SeriesID:     seriesID,
				EpisodeID:    episode.ID,
				Number:       episode.Number,
				Quality:      quality,
				ProtectOnly:  seriesID == centerSeriesID,
				Deferred:     item.deferred,
				PrefetchOnly: item.prefetchOnly,
				Priority:     item.priority,
			})
		}

		// The renderer supplies the playing episode ID. Publishing it first gives
		// MediaServer an immediate protection key and cancels every stale feed
		// download without waiting for adjacent-series metadata requests.
		if centerEpisodeID != "" {
			publish(PrefetchTask{
				SeriesID:    centerSeriesID,
				EpisodeID:   centerEpisodeID,
				Number:      1,
				Quality:     quality,
				ProtectOnly: true,
			})
		}

		var workers sync.WaitGroup
		for _, item := range items {
			if item.id == centerSeriesID && centerEpisodeID != "" {
				continue
			}
			workers.Add(1)
			go func(item warmupItem) {
				defer workers.Done()
				resolveItem(item)
			}(item)
		}
		workers.Wait()
	}()

	return nil
}

func (s *HongguoService) GetMediaDiagnostics(ctx context.Context) (MediaDiagnosticsDTO, error) {
	return s.media.MediaDiagnostics(), nil
}

func (s *HongguoService) RecordFeedMetrics(ctx context.Context, metrics []FeedMetricDTO) error {
	if len(metrics) == 0 {
		return nil
	}
	if len(metrics) > 32 {
		metrics = metrics[len(metrics)-32:]
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range metrics {
		metric := &metrics[i]
		metric.Type = truncateString(metric.Type, 32)
		metric.Event = truncateString(metric.Event, 40)
		metric.Reason = truncateString(metric.Reason, 64)
		metric.SessionID = truncateString(metric.SessionID, 48)
		metric.SeriesID = truncateString(metric.SeriesID, 64)
		metric.EpisodeID = truncateString(metric.EpisodeID, 64)
		metric.InputSource = truncateString(metric.InputSource, 24)
		metric.NetworkProfile = truncateString(metric.NetworkProfile, 16)
		metric.StreamMode = truncateString(metric.StreamMode, 24)
		metric.Quality = truncateString(metric.Quality, 16)
		metric.FirstFrameSource = truncateString(metric.FirstFrameSource, 24)
		if metric.At == "" {
			metric.At = now
		} else {
			metric.At = truncateString(metric.At, 48)
		}
	}
	return s.store.AppendFeedMetrics(metrics)
}
