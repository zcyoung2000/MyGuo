package media

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
)

const (
	// The reference client submits at most fifteen models per request.
	multiVideoModelBatchSize = hgapi.PlaybackBatchSize
	// multiVideoModelWindow is the i+1/i+2 lookahead requested while an episode
	// plays. It deliberately matches the conservative batch above.
	multiVideoModelWindow      = 2
	multiVideoModelPath        = hgapi.PlaybackPath
	multiVideoModelOrigin      = "https://api5-normal-lq.fqnovel.com"
	multiVideoModelHTTPTimeout = 8 * time.Second
	maxPlaybackResponseBytes   = int64(8 * 1024 * 1024)
	sdkVersionName             = "7.3.9.32"
)

type MultiVideoModelRequest struct {
	MixedVideoIDMap map[string][]string `json:"mixed_video_id_map"`
	DRScene         string              `json:"dr_scene"`
	BizParam        map[string]any      `json:"biz_param"`
}

type multiVideoModelResponse struct {
	Code    json.RawMessage            `json:"code"`
	Message string                     `json:"message"`
	Data    map[string]json.RawMessage `json:"data"`
}

type multiModelTask struct {
	episodes []model.Episode
}

type multiModelLoad struct {
	done      chan struct{}
	setCancel chan context.CancelFunc
}

type multiModelWindow struct {
	tasks []multiModelTask
}

// MultiVideoModelBatcher is isolated from media-file prefetching. Its HTTP work
// only fills the existing playback-model cache, so normal playback and model
// prefetch share deduplication and expiry instead of creating a second cache.
type MultiVideoModelBatcher struct {
	client             *hgapi.Client
	httpClient         *http.Client
	multiVideoModelURL string
	deviceIdentity     func() (deviceID, installID, cdid, openudid string)

	mu       sync.Mutex
	loads    map[string]*multiModelLoad
	window   *multiModelWindow
	windowID uint64
}

func NewMultiVideoModelBatcher(client *hgapi.Client, httpClient *http.Client) *MultiVideoModelBatcher {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: multiVideoModelHTTPTimeout}
	}
	return &MultiVideoModelBatcher{
		client: client, httpClient: httpClient, multiVideoModelURL: multiVideoModelOrigin,
		loads: make(map[string]*multiModelLoad),
	}
}

// NextEpisodes returns at most the following two playable episodes, preserving
// episode order and excluding duplicates.
func NextEpisodes(episodes []model.Episode, currentNumber int) []model.Episode {
	if len(episodes) == 0 || multiVideoModelWindow <= 0 {
		return nil
	}
	ordered := append([]model.Episode(nil), episodes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Number < ordered[j].Number })
	seen := make(map[int]bool, len(ordered))
	out := make([]model.Episode, 0, multiVideoModelWindow)
	for _, episode := range ordered {
		if episode.Number <= currentNumber || !episode.Playable || strings.TrimSpace(episode.ID) == "" {
			continue
		}
		if seen[episode.Number] {
			continue
		}
		seen[episode.Number] = true
		out = append(out, episode)
		if len(out) == multiVideoModelWindow {
			break
		}
	}
	return out
}

func multiVideoModelBody(episodes []model.Episode) ([]byte, error) {
	if len(episodes) == 0 || len(episodes) > multiVideoModelBatchSize {
		return nil, fmt.Errorf("multi video model batch must contain 1..%d episodes", multiVideoModelBatchSize)
	}
	ids := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		ids = append(ids, episode.ID)
	}
	return hgapi.PlaybackBody(ids, 1004)
}

func decodeMultiVideoModel(body []byte, episodeID string) (*model.Playback, error) {
	var decoded model.Playback
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("multi video model %s is not JSON: %w", episodeID, err)
	}
	decoded.EpisodeID = episodeID
	if len(decoded.Streams) == 0 {
		return nil, fmt.Errorf("multi video model %s has no stream", episodeID)
	}
	return &decoded, nil
}

func parseMultiVideoModels(body []byte, episodes []model.Episode) (map[string]*model.Playback, error) {
	ids := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	return hgapi.ParseVideoModels(body, ids)
}

func (b *MultiVideoModelBatcher) setDeviceIdentity(identity func() (deviceID, installID, cdid, openudid string)) {
	b.mu.Lock()
	b.deviceIdentity = identity
	b.mu.Unlock()
}

func (b *MultiVideoModelBatcher) forgetWindow() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.windowID++
	b.window = nil
	for _, load := range b.loads {
		if cancel, ok := <-load.setCancel; ok {
			cancel()
		}
		select {
		case <-load.done:
		default:
			close(load.done)
		}
	}
	return b.windowID
}

func (b *MultiVideoModelBatcher) acquireLoads(episodes []model.Episode) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.loads == nil {
		b.loads = make(map[string]*multiModelLoad)
	}
	for _, episode := range episodes {
		if _, active := b.loads[episode.ID]; active {
			return false
		}
	}
	for _, episode := range episodes {
		load := &multiModelLoad{done: make(chan struct{}), setCancel: make(chan context.CancelFunc, 1)}
		b.loads[episode.ID] = load
	}
	return true
}

func (b *MultiVideoModelBatcher) releaseLoads(loadIDs []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range loadIDs {
		if load := b.loads[id]; load != nil {
			select {
			case cancel := <-load.setCancel:
				cancel()
			default:
			}
			select {
			case <-load.done:
			default:
				close(load.done)
			}
			delete(b.loads, id)
		}
	}
}

func (b *MultiVideoModelBatcher) ReplaceWindow(episodes []model.Episode) uint64 {
	window := multiModelWindow{tasks: make([]multiModelTask, 0, (len(episodes)+multiVideoModelBatchSize-1)/multiVideoModelBatchSize)}
	for start := 0; start < len(episodes); start += multiVideoModelBatchSize {
		end := min(start+multiVideoModelBatchSize, len(episodes))
		window.tasks = append(window.tasks, multiModelTask{episodes: append([]model.Episode(nil), episodes[start:end]...)})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.windowID++
	b.window = &window
	for _, load := range b.loads {
		select {
		case cancel := <-load.setCancel:
			cancel()
		default:
		}
		select {
		case <-load.done:
		default:
			close(load.done)
		}
	}
	return b.windowID
}

func (b *MultiVideoModelBatcher) snapshotWindow() (uint64, []multiModelTask, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.window == nil {
		return 0, nil, false
	}
	return b.windowID, b.window.tasks, true
}

func (b *MultiVideoModelBatcher) windowUnchanged(id uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.windowID == id
}

func (b *MultiVideoModelBatcher) Run(ctx context.Context, remember func(string, *model.Playback)) {
	if b == nil || b.client == nil {
		return
	}
	windowID, tasks, ok := b.snapshotWindow()
	if !ok {
		return
	}
	for _, task := range tasks {
		if ctx.Err() != nil || !b.windowUnchanged(windowID) {
			return
		}
		b.runTask(ctx, task, remember, windowID)
	}
	b.mu.Lock()
	if b.windowID == windowID {
		b.window = nil
	}
	b.mu.Unlock()
}

func (b *MultiVideoModelBatcher) runTask(ctx context.Context, task multiModelTask, remember func(string, *model.Playback), windowID uint64) {
	if !b.acquireLoads(task.episodes) {
		return
	}
	loadIDs := make([]string, 0, len(task.episodes))
	for _, episode := range task.episodes {
		loadIDs = append(loadIDs, episode.ID)
	}
	defer b.releaseLoads(loadIDs)
	loads := make([]*multiModelLoad, 0, len(task.episodes))
	b.mu.Lock()
	for _, episode := range task.episodes {
		if load := b.loads[episode.ID]; load != nil {
			loads = append(loads, load)
		}
	}
	b.mu.Unlock()
	fetchCtx, fetchCancel := context.WithCancel(ctx)
	defer fetchCancel()
	for _, load := range loads {
		select {
		case <-load.done:
			fetchCancel()
			return
		case load.setCancel <- fetchCancel:
		}
		defer func() {
			select {
			case <-load.done:
			default:
				close(load.done)
			}
		}()
	}

	playbacks, err := b.fetch(fetchCtx, task.episodes)
	if err != nil || ctx.Err() != nil || !b.windowUnchanged(windowID) {
		return
	}
	for _, episode := range task.episodes {
		if playback := playbacks[episode.ID]; playback != nil {
			remember(episode.ID, playback)
		}
	}
}

func (b *MultiVideoModelBatcher) fetch(ctx context.Context, episodes []model.Episode) (map[string]*model.Playback, error) {
	b.mu.Lock()
	identity := b.deviceIdentity
	b.mu.Unlock()
	var deviceID, installID, cdid, openudid string
	if identity != nil {
		deviceID, installID, cdid, openudid = identity()
	}
	ids := make([]string, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	options := hgapi.PlaybackOptions{}
	if b.multiVideoModelURL != "" && b.multiVideoModelURL != multiVideoModelOrigin {
		options.Origin = b.multiVideoModelURL
	}
	if deviceID != "" && installID != "" {
		options.Profile = &hgapi.DeviceProfile{DeviceID: deviceID, InstallID: installID, CDID: cdid, OpenUDID: openudid}
	}
	return b.client.MultiVideoModels(ctx, ids, options)
}
