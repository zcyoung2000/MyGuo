// Package playback owns video sessions independently of UI and transport. Only
// its event loop touches engines; asynchronous work is checked against the
// scene and selected slot before it can affect playback or persistence.
package playback

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"myguo/internal/avkit"
	"myguo/internal/hgapi"
	"myguo/internal/ipc"
	"myguo/internal/media"
)

type Backend interface {
	PlayEpisode(context.Context, string, string, int, string, bool) (*ipc.PlaybackDTO, error)
	GetSeriesDetail(context.Context, string) (*ipc.SeriesDetailDTO, error)
	GetNextSeason(context.Context, string) (*ipc.SeriesCard, error)
	GetRecommendFeed(context.Context, string, string) (*ipc.RecommendFeedResult, error)
	RestoreRecommendFeed(context.Context, string) (*ipc.RecommendFeedResult, error)
	ConsumeRecommendFeedItem(context.Context, string, string) error
	UpdateFeedPreloadWindow(context.Context, int64, string, string, []media.FeedPreloadItem) ([]media.FeedPreloadStatus, error)
	GetProfile(context.Context) (ipc.UserProfile, error)
	SaveProgress(context.Context, string, string, string, string, int, int, float64, float64) error
	SaveProgressForScope(context.Context, string, string, string, string, string, int, int, float64, float64) error
	PlaybackScope() string
	FlushPendingProgress()
	IsFavorite(context.Context, string) (bool, error)
	ToggleFavorite(context.Context, string, string, string, string, int) (bool, error)
	ToggleLike(context.Context, string, string, string, string, int) (bool, error)
	GetEpisodeComments(context.Context, string, string, string) (*ipc.CommentPageDTO, error)
	GetEpisodeCommentReplies(context.Context, string, string, string, string) (*ipc.CommentPageDTO, error)
	GetDanmaku(context.Context, string, string, int64, int64) (*ipc.DanmakuPage, error)
	SendDanmaku(context.Context, string, string, string, int64, string) (*ipc.DanmakuSendResult, error)
	LikeDanmaku(context.Context, string, string, bool) (bool, error)
}

type Mode string

const (
	Idle   Mode = "idle"
	Feed   Mode = "feed"
	Series Mode = "series"
)

type Progress struct {
	SeriesID   string
	EpisodeNum int
	Position   float64
	Session    int64
	Sequence   uint64
}
type Hooks struct {
	Changed        func()
	Progress       func(Progress)
	LibraryChanged func()
	System         func(State)
	Closed         func(int64)
	Fullscreen     func()
}

type State struct {
	Scene              uint64
	DanmakuVersion     uint64
	DanmakuLaunchAfter float64
	ClockEpoch         uint64
	scope              string
	Mode               Mode
	Session            int64
	Revision           uint64
	Book               ipc.SeriesCard
	Playback           *ipc.PlaybackDTO
	Episodes           []ipc.EpisodeDTO
	Engine             avkit.Player
	Status             avkit.Status
	Updated            time.Time
	Preferences        Preferences
	Loading            bool
	Error              string
	Notice             string
	Paused             bool
	Favorite           bool
	Channel            string
	Index              int
	FeedCount          int
	HasMore            bool
	Panel              string
	CommentItems       []ipc.CommentDTO
	CommentTotal       int64
	CommentLoading     bool
	CommentError       string
	CommentHasMore     bool
	Replies            map[string]ReplyPage
	DanmakuItems       []ipc.Danmaku
	DanmakuError       string
	Sending            bool
	SentSequence       uint64
	SentText           string
	Transition         int
	TransitionID       uint64
	TransitionFrame    []byte
	FirstFrameMS       int64
}

type ReplyPage struct {
	Items            []ipc.CommentDTO
	Loading, HasMore bool
	Cursor, Error    string
}

type slot struct {
	key, seriesID   string
	number          int
	card            ipc.SeriesCard
	detail          *ipc.SeriesDetailDTO
	dto             *ipc.PlaybackDTO
	engine          avkit.Player
	ctx             context.Context
	cancel          context.CancelFunc
	loading         bool
	resume          float64
	seeked          bool
	started         time.Time
	used            time.Time
	fallback        int
	first           bool
	sourceStarted   time.Time
	seekPending     bool
	seekTarget      float64
	seekAt          time.Time
	preroll         bool
	warming         bool
	bufferingSince  time.Time
	clockAdvanced   time.Time
	clockPosition   float64
	stallResumed    bool
	ended           bool
	err             string
	unavailable     bool
	accessLimited   bool
	sourceURL       string
	resolveEpisode  string
	resolveQuality  string
	resolveAttempts int
	retryAt         time.Time
	playStarted     bool
	startReloaded   bool
}

type Controller struct {
	backend                           Backend
	factory                           func() (avkit.Player, error)
	hooks                             Hooks
	mu                                sync.RWMutex
	visible                           State
	workMu                            sync.Mutex
	work                              []func()
	wake                              chan struct{}
	done                              chan struct{}
	stop                              chan struct{}
	closing                           bool
	state                             State
	scene                             uint64
	root                              context.Context
	cancel                            context.CancelFunc
	slots                             map[string]*slot
	current                           *slot
	floating                          avkit.Player
	pendingIndex                      int
	desired                           string
	feed                              []ipc.SeriesCard
	cursor                            string
	feedLoading                       bool
	feedSeen                          map[string]bool
	feedCursors                       map[string]bool
	account                           string
	sequence                          uint64
	sentSequence                      uint64
	lastSave, lastSystem, lastPreload time.Time
	preloadVersion                    int64
	preloadLoading                    bool
	preloadAdmitted                   bool
	uiSelection                       uint64
	commentCursors                    map[string]bool
	replyCursors                      map[string]map[string]bool
	commentCursor                     string
	commentSeen                       map[string]bool
	danmakuWindows                    map[int64]bool
	danmakuLoading                    map[int64]bool
	socialBusy                        map[string]bool
	progressMu                        sync.Mutex
	progressQueue                     []progressWrite
	progressWake                      chan struct{}
	progressClosing                   bool
	progressDone                      chan struct{}
	preferencesPath                   string
}

type progressWrite struct {
	book               ipc.SeriesCard
	dto                ipc.PlaybackDTO
	position, duration float64
	account            string
}

func New(backend Backend, factory func() (avkit.Player, error), preferencesPath string, hooks Hooks) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	p := LoadPreferences(preferencesPath)
	c := &Controller{backend: backend, factory: factory, hooks: hooks, wake: make(chan struct{}, 1), done: make(chan struct{}), stop: make(chan struct{}), root: ctx, cancel: cancel, slots: map[string]*slot{}, preferencesPath: preferencesPath, progressWake: make(chan struct{}, 1), progressDone: make(chan struct{}), state: State{Mode: Idle, Preferences: p}}
	c.visible = c.state
	go c.saveLoop()
	go c.run()
	return c
}

func (c *Controller) Snapshot() State { c.mu.RLock(); defer c.mu.RUnlock(); return c.visible }
func (c *Controller) post(f func()) bool {
	c.workMu.Lock()
	defer c.workMu.Unlock()
	if c.closing {
		return false
	}
	c.work = append(c.work, f)
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return true
}
func (c *Controller) run() {
	defer close(c.done)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-c.stop:
			c.dispose()
			return
		case <-c.wake:
			c.workMu.Lock()
			work := c.work
			c.work = nil
			c.workMu.Unlock()
			for _, f := range work {
				f()
			}
			c.publish()
		case now := <-tick.C:
			c.tick(now)
		}
	}
}
func (c *Controller) publish() {
	if c.state.Replies == nil {
		c.state.Replies = map[string]ReplyPage{}
	}
	// Internal preload acknowledgments and an unchanged paused clock must not
	// rebuild the entire native view. Updated anchors time only when visible
	// state actually changes; animated danmaku uses paint-only frames.
	c.mu.RLock()
	previous := c.visible
	c.mu.RUnlock()
	candidate := c.state
	candidate.Revision = previous.Revision
	candidate.Updated = previous.Updated
	if reflect.DeepEqual(candidate, previous) {
		return
	}
	c.state.Revision++
	s := c.state
	// Readers never receive actor-owned mutable slices or maps.
	s.Episodes = slices.Clone(s.Episodes)
	s.CommentItems = slices.Clone(s.CommentItems)
	s.DanmakuItems = slices.Clone(s.DanmakuItems)
	s.Replies = make(map[string]ReplyPage, len(c.state.Replies))
	for k, v := range c.state.Replies {
		v.Items = slices.Clone(v.Items)
		s.Replies[k] = v
	}
	s.FeedCount = len(c.feed)
	c.mu.Lock()
	c.visible = s
	c.mu.Unlock()
	if c.hooks.Changed != nil {
		c.hooks.Changed()
	}
}
func (c *Controller) reset(mode Mode, session int64) {
	c.saveCurrent()
	c.scene++
	c.cancel()
	for _, s := range c.slots {
		if s.engine != nil {
			s.engine.Pause()
			_ = s.engine.Close()
		}
		s.cancel()
	}
	c.root, c.cancel = context.WithCancel(context.Background())
	if c.floating != nil {
		c.floating.Pause()
		_ = c.floating.Close()
		c.floating = nil
	}
	c.slots = map[string]*slot{}
	c.current = nil
	c.desired = ""
	c.feed = nil
	c.cursor = ""
	c.feedLoading = false
	c.pendingIndex = -1
	c.preloadLoading = false
	c.preloadAdmitted = false
	c.feedSeen = map[string]bool{}
	c.feedCursors = map[string]bool{}
	c.socialBusy = map[string]bool{}
	c.clearEpisodeUI()
	c.state = State{Scene: c.scene, Mode: mode, Session: session, Preferences: c.state.Preferences, Channel: c.state.Channel, HasMore: true, Paused: false}
	c.state.Preferences.Clear = false
	c.account = c.backend.PlaybackScope()
	if binder, ok := c.backend.(interface {
		BindPlaybackContext(context.Context, string) context.Context
	}); ok {
		c.root = binder.BindPlaybackContext(c.root, c.account)
	}
	c.state.scope = c.account
	c.preloadVersion++
	// Empty windows cancel stale speculative work. These requests use a
	// monotonic version even across scenes, and don't get canceled by reset.
	version := c.preloadVersion
	go c.backend.UpdateFeedPreloadWindow(context.Background(), version, "", "", nil)
}
func (c *Controller) ShowFeed(channel string) {
	c.post(func() {
		c.reset(Feed, 0)
		c.state.Channel = channel
		c.state.Loading = true
		scene, ctx := c.scene, c.root
		go func() {
			restored, err := c.backend.RestoreRecommendFeed(ctx, channel)
			c.post(func() {
				if c.scene != scene {
					return
				}
				if err == nil && restored != nil {
					c.appendFeed(restored.Items)
					c.state.HasMore = restored.HasMore
					c.cursor = restored.Cursor
					if restored.Cursor != "" {
						c.feedCursors[restored.Cursor] = true
					}
				} else if err != nil {
					log.Printf("[native-player] restore recommendations channel=%q: %v", channel, err)
				}
				if len(c.feed) > 0 {
					c.selectFeed(0)
				}
				c.fetchFeed()
			})
		}()
	})
}
func (c *Controller) Play(seriesID string, number int, resume float64, session int64) {
	c.post(func() {
		c.reset(Series, session)
		c.selectSlot(ipc.SeriesCard{ID: seriesID}, max(1, number), resume, 0)
	})
}
func (c *Controller) Stop() { c.post(func() { c.reset(Idle, 0); c.state.HasMore = false; c.system() }) }
func (c *Controller) selectSlot(card ipc.SeriesCard, number int, resume float64, direction int) {
	c.selectSlotWithQuality(card, number, resume, direction, false)
}
func (c *Controller) selectSlotWithQuality(card ipc.SeriesCard, number int, resume float64, direction int, forceQuality bool) {
	c.preloadAdmitted = false
	c.uiSelection++
	c.saveCurrent()
	// Snapshot once before retiring a surface; a frozen outgoing frame keeps
	// rapid swipes smooth without retaining a fourth decoder or extra audio.
	frame := c.state.TransitionFrame
	if direction == 0 || c.state.Mode != Feed {
		frame = nil
	} else if old := c.current; old != nil && old.engine != nil {
		status := old.engine.Snapshot()
		if status.PictureInPicture {
			frame = nil
		} else if status.Ready {
			if captured := old.engine.CaptureFrame(); len(captured) > 0 {
				frame = captured
			}
		}
	}
	episodeID := ""
	if old := c.current; old != nil && old.seriesID == card.ID && old.number == number && old.dto != nil {
		episodeID = old.dto.EpisodeID
	}
	if old := c.current; old != nil && old.engine != nil {
		old.used = time.Now()
		status := old.engine.Snapshot()
		old.engine.Pause()
		old.warming = false
		if status.PictureInPicture {
			c.floating = old.engine
			old.engine = nil
		}
	}
	c.clearEpisodeUI()
	c.state.Book = card
	c.state.Playback = nil
	c.state.Engine = c.floating
	c.state.Status = avkit.Status{}
	c.state.Loading = true
	c.state.Error = ""
	c.state.Notice = ""
	c.state.Transition = direction
	c.state.TransitionID = c.uiSelection
	c.state.TransitionFrame = frame
	c.state.FirstFrameMS = 0
	key := fmt.Sprintf("%s/%d/%s", card.ID, number, c.state.Preferences.Quality)
	c.desired = key
	// Drop obsolete decoders before allocating the new selection.
	c.current = &slot{key: key, seriesID: card.ID, number: number}
	c.trimSlots()
	c.current = nil
	existing := c.slots[key]
	if forceQuality && existing != nil && existing.dto != nil && existing.dto.Quality != c.state.Preferences.Quality {
		existing.cancel()
		if existing.engine != nil {
			_ = existing.engine.Close()
		}
		delete(c.slots, key)
		existing = nil
	}
	if existing != nil {
		c.current = existing
		existing.used = time.Now()
		existing.started = time.Now()
		existing.first = false
		existing.playStarted = false
		existing.startReloaded = false
		existing.ended = false
		if resume >= 0 {
			existing.resume = resume
			existing.seeked = false
		}
		c.applySlot(existing)
	} else {
		c.resolve(card, number, resume, episodeID)
	}
	c.system()
}
func (c *Controller) resolve(card ipc.SeriesCard, number int, resume float64, episodeID string) {
	key := fmt.Sprintf("%s/%d/%s", card.ID, number, c.state.Preferences.Quality)
	if c.slots[key] != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.root)
	s := &slot{key: key, seriesID: card.ID, number: number, card: card, ctx: ctx, cancel: cancel, loading: true, resume: resume, started: time.Now(), used: time.Now()}
	c.slots[key] = s
	if c.desired == key {
		c.current = s
	}
	scene := c.scene
	// Reuse an already resolved episode when changing rendition. Negotiating
	// with an empty ID invokes offline bootstrap, which may choose a different
	// cached quality instead of the requested rendition.
	s.resolveEpisode, s.resolveQuality = episodeID, c.state.Preferences.Quality
	c.resolveSource(s)
	// Details must not block source negotiation / first frame.
	go func() {
		detail, err := c.backend.GetSeriesDetail(ctx, card.ID)
		c.post(func() {
			if c.scene != scene || c.slots[key] != s || ctx.Err() != nil || err != nil || detail == nil {
				return
			}
			s.detail = detail
			s.card = ipc.SeriesCard{ID: detail.ID, Title: detail.Title, Description: detail.Description, CoverURL: detail.CoverURL, EpisodeCount: detail.EpisodeCount, Status: detail.Status}
			if c.desired == key {
				c.state.Book = s.card
				c.state.Episodes = detail.Episodes
				c.state.Favorite = detail.IsFavorite
				if value, err := c.backend.IsFavorite(c.root, s.seriesID); err == nil {
					c.state.Favorite = value
				}
			}
			c.prepareNeighbors()
		})
	}()
}

// Only the selected slot spends its retry budget. Prefetch failures remain
// recoverable on promotion; navigating away or closing cancels scheduled work.
func (c *Controller) resolveSource(s *slot) {
	s.loading = true
	s.retryAt = time.Time{}
	s.resolveAttempts++
	scene, ctx, key := c.scene, s.ctx, s.key
	id, episode, number, quality := s.seriesID, s.resolveEpisode, s.number, s.resolveQuality
	go func() {
		dto, err := c.backend.PlayEpisode(ctx, id, episode, number, quality, true)
		c.post(func() {
			if c.scene != scene || c.slots[key] != s || ctx.Err() != nil {
				return
			}
			s.loading = false
			if err != nil || dto == nil {
				if err == nil {
					err = fmt.Errorf("没有可用的播放地址")
				}
				s.err = fmt.Sprint(err)
				s.unavailable = errors.Is(err, ipc.ErrNoPlayableEpisodes) || errors.Is(err, hgapi.ErrContentUnavailable)
				s.accessLimited = errors.Is(err, ipc.ErrPlaybackAccessLimited)
				log.Printf("[native-player] resolve/load series=%q attempt=%d: %s", id, s.resolveAttempts, s.err)
				if !s.unavailable && !errors.Is(err, hgapi.ErrAuthenticationRequired) && s.resolveAttempts < 3 {
					s.retryAt = time.Now().Add(time.Duration(1<<(s.resolveAttempts-1)) * 500 * time.Millisecond)
				}
				if c.desired == key {
					c.applySlot(s)
				} else if s.unavailable && c.state.Mode == Feed {
					c.discardUnavailableCandidate(s)
				}
				return
			}
			s.err = ""
			s.dto = dto
			s.number = dto.EpisodeNum
			if c.desired == key {
				c.state.Error = ""
				c.applySlot(s)
			}
			c.updatePreload()
		})
	}()
}
func (c *Controller) applySlot(s *slot) {
	if s.dto == nil {
		if s.err != "" {
			if !s.retryAt.IsZero() || s.loading {
				c.state.Loading = true
				c.state.Error = ""
				return
			}
			c.state.Error = sourceLoadMessage(s)
			c.state.Loading = false
			if s.unavailable && c.state.Mode == Feed {
				c.skipUnavailableFeed(s)
			}
		}
		return
	}
	c.current = s
	c.state.Playback = s.dto
	c.state.Book = s.card
	if s.detail != nil {
		c.state.Episodes = s.detail.Episodes
		c.state.Favorite = s.detail.IsFavorite
		if value, err := c.backend.IsFavorite(c.root, s.seriesID); err == nil {
			c.state.Favorite = value
		}
	}
	if c.floating != nil {
		if s.engine != nil && s.engine != c.floating {
			_ = s.engine.Close()
		}
		s.engine = c.floating
		c.floating = nil
		s.seeked = false
		url := nativeSourceURL(s.dto)
		if s.preroll && s.dto.HLSStreamURL != "" {
			url = s.dto.HLSStreamURL
		}
		if err := s.engine.Load(url); err != nil {
			c.state.Error = "视频加载失败，请重试"
			c.state.Loading = false
			return
		}
		s.sourceURL = url
		s.sourceStarted = time.Now()
	}
	if s.engine == nil {
		if !c.loadEngine(s, false) {
			return
		}
	}
	c.state.Engine = s.engine
	if !s.seeked {
		position := s.resume
		if position < 0 {
			position = s.dto.LastPosition
		}
		// Preroll has already decoded this position; an exact seek here would
		// discard the prepared pipeline immediately before promotion.
		status := s.engine.Snapshot()
		if math.Abs(status.Position-math.Max(0, position)) > .1 || (position > 0 && !s.preroll && !status.Ready) {
			c.seek(s, position)
		}
		s.seeked = true
	}
	c.configure(s)
	s.warming = false
	if !c.state.Paused {
		s.engine.Play()
	} else {
		s.engine.Pause()
	}
	c.state.Loading = !s.first
	if c.state.Panel == "comments" {
		c.loadComments(false)
	}
	c.fetchDanmaku()
	c.prepareNeighbors()
}
func (c *Controller) configure(s *slot) {
	if s.engine != nil {
		p := c.state.Preferences
		s.engine.Configure(p.Rate, p.Volume, p.Muted || s != c.current, p.Fit)
	}
}
func (c *Controller) loadEngine(s *slot, warm bool) bool {
	engine, err := c.factory()
	if err != nil {
		s.err = fmt.Sprint(err)
		if c.current == s {
			log.Printf("[native-player] create engine: %s", s.err)
			c.state.Error = "播放器启动失败，请重新打开视频"
			c.state.Loading = false
		}
		return false
	}
	s.engine = engine
	c.configure(s)
	url := nativeSourceURL(s.dto)

	if err = engine.Load(url); err != nil {
		_ = engine.Close()
		s.engine = nil
		s.err = fmt.Sprint(err)
		if c.current == s {
			log.Printf("[native-player] resolve/load series=%q: %s", s.seriesID, s.err)
			c.state.Error = "视频加载失败，请检查网络后重试"
			c.state.Loading = false
		}
		return false
	}
	s.sourceURL = url
	s.sourceStarted = time.Now()
	if warm {
		engine.Pause()
		engine.Preroll()
		s.warming = true
	}
	return true
}
func (c *Controller) tick(now time.Time) {
	if c.state.Mode == Idle {
		return
	}
	if c.backend.PlaybackScope() != c.account {
		// The old clock must never become history for the newly selected account.
		c.current = nil
		c.reset(Idle, 0)
		c.state.Notice = "账号已切换，请重新选择视频"
		c.system()
		c.publish()
		return
	}
	s := c.current
	if s != nil && !s.retryAt.IsZero() && !s.loading && !now.Before(s.retryAt) {
		log.Printf("[native-player] retry source series=%q attempt=%d", s.seriesID, s.resolveAttempts+1)
		c.resolveSource(s)
	}
	if s != nil && s.engine != nil {
		previousStatus := c.state.Status
		status := playerStatus(s)
		if s.seekPending && status.Seekable > 0 && s.seekTarget > status.Seekable+.25 && s.dto != nil && s.dto.FullStreamURL != "" && s.sourceURL != s.dto.FullStreamURL {
			c.loadFullForSeek(s, s.seekTarget)
			c.publish()
			return
		}
		if s.seekPending {
			if math.Abs(status.Position-s.seekTarget) < 1 || (status.Ready && !status.Buffering && now.Sub(s.seekAt) > 3*time.Second) {
				s.seekPending = false
			} else {
				status.Position = s.seekTarget
			}
		}
		c.state.Status = status
		if !s.playStarted && status.Ready && status.Playing && status.Position > .05 {
			s.playStarted = true
			c.state.Loading = false
			log.Printf("[native-player] clock-start episode=%q elapsedMs=%d position=%.2f ahead=%.2f", s.dto.EpisodeID, now.Sub(s.started).Milliseconds(), status.Position, math.Max(0, status.Buffered-status.Position))
		}
		if c.incompleteHLSEnd(s, status) {
			// A growing playlist's current prefix can look like EOF to AVKit.
			// It must not skip the rest of the episode or advance to the next one.
			c.fallback(s, "HLS ended before the episode's expected duration")
			c.publish()
			return
		}
		c.traceBuffering(s, status, now)
		c.state.Updated = now
		if c.recoverStall(s, status, now) {
			c.publish()
			return
		}
		// Inline playback intent comes from our controls. A decoder's zero
		// rate during loading, seek or starvation is not a user pause. Only
		// AVKit's PiP controls can change intent outside the controller, and
		// a native pause must follow an observed playing state.
		if s.first && status.PictureInPicture && status.Ready && !status.Ended && !status.Buffering && !s.seekPending && c.state.Error == "" &&
			c.state.Paused == status.Playing && (status.Playing || previousStatus.Playing) {
			c.state.Paused = !status.Playing
			if c.state.Paused {
				c.state.Preferences.Clear = false
			}
			c.saveCurrent()
			c.system()
		}

		if c.state.Error == "" && (status.Error != "" || (!s.first && !status.Ready && now.Sub(s.sourceStarted) > c.sourceTimeout(s))) {
			c.fallback(s, status.Error)
			c.publish()
			return
		}
		if status.Ready && status.Error == "" && c.state.Error == "" && !s.first {
			s.first = true
			if c.state.Mode == Feed {
				channel, id, ctx := c.state.Channel, s.seriesID, c.root
				go func() {
					if err := c.backend.ConsumeRecommendFeedItem(ctx, channel, id); err != nil {
						log.Printf("[native-player] consume feed queue: %v", err)
					}
				}()
			}
			c.state.Loading = !c.state.Paused && !s.playStarted
			c.state.Error = ""
			c.state.FirstFrameMS = max(0, now.Sub(s.started).Milliseconds())
			log.Printf("[native-player] first-frame series=%q episode=%d elapsedMs=%d fallback=%d", s.seriesID, s.number, c.state.FirstFrameMS, s.fallback)
		}
		if status.Ended && c.state.Error == "" && !s.ended {
			s.ended = true
			c.saveCurrent()
			c.nextEpisode(1)
		}
		if now.Sub(c.lastSave) >= 5*time.Second && status.Ready && status.Playing {
			c.saveCurrent()
		}
		if now.Sub(c.lastSystem) >= time.Second {
			c.system()
		}
		c.fetchDanmaku()
	}
	if now.Sub(c.lastPreload) >= 500*time.Millisecond {
		c.updatePreload()
	}
	c.publish()
}
func (c *Controller) fallback(s *slot, reason string) {
	if s.dto == nil || s.engine == nil {
		return
	}
	urls := []string{s.dto.HLSStreamURL, s.dto.FullStreamURL}
	for s.fallback < len(urls) {
		url := urls[s.fallback]
		s.fallback++
		if url == "" || url == s.sourceURL {
			continue
		}
		position := math.Max(0, c.state.Status.Position)
		if s.seekPending {
			position = s.seekTarget
		} else if !s.first && !c.state.Status.Ready {
			position = math.Max(position, math.Max(0, s.resume))
		}
		log.Printf("[native-player] fallback episode=%q source=%d reason=%q", s.dto.EpisodeID, s.fallback, reason)
		s.engine.Pause()
		if err := s.engine.Load(url); err != nil {
			continue
		}
		c.seek(s, position)
		c.configure(s)
		if !c.state.Paused {
			s.engine.Play()
		}
		s.sourceURL = url
		s.first = false
		s.playStarted = false
		c.state.Loading = true
		c.state.Error = ""
		s.sourceStarted = time.Now()
		return
	}
	c.state.Loading = false
	c.state.Error = "视频暂时无法播放，请重试"
	log.Printf("[native-player] playback exhausted episode=%q reason=%q", s.dto.EpisodeID, reason)
	s.engine.Pause()
}
func (c *Controller) system() {
	c.lastSystem = time.Now()
	if c.hooks.System != nil {
		c.hooks.System(c.state)
	}
}
func (c *Controller) clearEpisodeUI() {
	c.state.CommentItems = nil
	c.state.CommentTotal = 0
	c.state.CommentLoading = false
	c.state.CommentError = ""
	c.state.CommentHasMore = false
	c.state.Replies = map[string]ReplyPage{}
	c.commentCursor = ""
	c.commentCursors = map[string]bool{}
	c.replyCursors = map[string]map[string]bool{}
	c.commentSeen = map[string]bool{}
	c.state.DanmakuItems = nil
	c.state.DanmakuError = ""
	c.danmakuWindows = map[int64]bool{}
	c.danmakuLoading = map[int64]bool{}
	c.state.Sending = false
}
func (c *Controller) Command(command string, value any) {
	c.post(func() {
		s := c.current
		switch command {
		case "play", "pause", "toggle", "surface", "next", "prev", "channel":
			log.Printf("[native-player] control command=%q paused=%t loading=%t", command, c.state.Paused, c.state.Loading)
		}
		switch command {
		case "close":
			if c.state.Panel != "" {
				c.state.Panel = ""
			} else if c.hooks.Closed != nil {
				c.hooks.Closed(c.state.Session)
			}
		case "refresh":
			if c.state.Mode == Feed {
				channel := c.state.Channel
				c.reset(Feed, 0)
				c.state.Channel = channel
				c.state.Loading = true
				c.fetchFeed()
			}
		case "retry":
			c.retrySelection()
		case "surface":
			c.surfaceClick()
		case "toggle", "play", "pause":
			if c.state.Error != "" && command != "pause" {
				c.state.Paused = false
				c.retrySelection()
				break
			}
			paused := command == "pause" || (command == "toggle" && !c.state.Paused)
			if command == "toggle" && c.state.Status.Ended {
				paused = false
			}
			c.setPaused(paused)
		case "next", "prev":
			delta := 1
			if command == "prev" {
				delta = -1
			}
			if c.state.Mode == Feed {
				c.selectFeed(c.state.Index + delta)
			} else {
				c.nextEpisode(delta)
			}
		case "nextEpisode":
			c.nextEpisode(1)
		case "prevEpisode":
			c.nextEpisode(-1)
		case "seek", "seekBy":
			if s != nil && s.engine != nil {
				n := number(value)
				if math.IsNaN(n) || math.IsInf(n, 0) {
					return
				}
				if command == "seekBy" {
					n += c.state.Status.Position
				}
				n = math.Max(0, n)
				if c.state.Status.Duration > 0 {
					n = math.Min(n, c.state.Status.Duration)
				}
				if s.first {
					c.state.Loading = false
				}
				c.seek(s, n)
				c.state.Status.Position = n
				c.state.Updated = time.Now()
				s.ended = false
				c.saveCurrent()
				c.fetchDanmaku()
				c.system()
			}
		case "speed":
			n := number(value)
			if n >= 0.25 && n <= 2 {
				c.state.Preferences.Rate = n
				c.configureAll()
				c.persistPreferences()
				c.system()
			}
		case "volume":
			c.state.Preferences.Volume = finiteClamp(number(value), 0, 1, c.state.Preferences.Volume)
			c.state.Preferences.Muted = false
			c.configureAll()
			c.persistPreferences()
		case "mute":
			c.state.Preferences.Muted = !c.state.Preferences.Muted
			c.configureAll()
			c.persistPreferences()
		case "fit":
			if fit, ok := value.(string); ok && (fit == "cover" || fit == "contain") {
				c.state.Preferences.Fit = fit
				c.configureAll()
				c.persistPreferences()
			}
		case "quality":
			if quality, ok := value.(string); ok && s != nil && (quality != c.state.Preferences.Quality || (s.dto != nil && quality != s.dto.Quality)) {
				c.state.Preferences.Quality = quality
				c.persistPreferences()
				c.selectSlotWithQuality(s.card, s.number, c.state.Status.Position, 0, true)
			}
		case "episode":
			if s != nil {
				n := int(number(value))
				if n >= 1 && (c.state.Book.EpisodeCount <= 0 || n <= c.state.Book.EpisodeCount) {
					c.selectSlot(s.card, n, -1, 0)
				}
			}
		case "pip":
			if s != nil && s.engine != nil {
				engine, scene, key := s.engine, c.scene, c.desired
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					err := engine.TogglePictureInPicture(ctx)
					if err != nil {
						c.post(func() {
							if c.scene == scene && c.desired == key {
								log.Printf("[native-player] picture in picture: %v", err)
								c.state.Notice = "画中画暂时无法开启，请在视频开始播放后重试"
							}
						})
					}
				}()
			}
		case "fullscreen":
			if c.hooks.Fullscreen != nil {
				c.hooks.Fullscreen()
			}
		case "panel":
			if panel, ok := value.(string); ok {
				if c.state.Panel == panel {
					panel = ""
				}
				c.state.Panel = panel
				c.system()
				if panel == "comments" && len(c.state.CommentItems) == 0 {
					c.loadComments(false)
				}
			}
		case "clear":
			if clear, ok := value.(bool); ok {
				c.state.Preferences.Clear = clear
			} else {
				c.state.Preferences.Clear = !c.state.Preferences.Clear
			}
			if c.state.Preferences.Clear {
				c.state.Panel = ""
			}
			c.system()
		case "danmaku":
			c.state.Preferences.Danmaku.Enabled = !c.state.Preferences.Danmaku.Enabled
			c.persistPreferences()
			c.fetchDanmaku()
		case "preferences":
			if p, ok := value.(Preferences); ok {
				c.state.Preferences = p.Normalized()
				c.configureAll()
				c.persistPreferences()
			}
		case "dismiss":
			c.state.Notice = ""
		}
	})
}
func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}
func (c *Controller) configureAll() {
	for _, s := range c.slots {
		c.configure(s)
	}
}
func (c *Controller) nextEpisode(delta int) {
	s := c.current
	if s == nil {
		return
	}
	n := s.number + delta
	if n < 1 {
		c.state.Notice = "已经是第一集"
		return
	}
	if c.state.Book.EpisodeCount > 0 && n > c.state.Book.EpisodeCount {
		if delta < 0 {
			return
		}
		scene, key, clockEpoch, ctx := c.scene, c.desired, c.state.ClockEpoch, c.root
		c.state.Loading = true
		go func() {
			next, err := c.backend.GetNextSeason(ctx, s.seriesID)
			c.post(func() {
				if c.scene != scene || c.desired != key || c.state.ClockEpoch != clockEpoch {
					return
				}
				c.state.Loading = false
				if err != nil {
					log.Printf("[native-player] next season series=%q: %v", s.seriesID, err)
					if c.state.Mode == Feed && c.state.Status.Ended {
						c.selectFeed(c.state.Index + 1)
					}
					return
				}
				if next == nil || next.ID == "" {
					if c.state.Mode == Feed && c.state.Status.Ended {
						c.selectFeed(c.state.Index + 1)
						return
					}
					c.state.Notice = "本剧已播放完"
					if c.state.Status.Ended {
						c.state.Paused = true
						c.state.Preferences.Clear = false
					}
					c.system()
					return
				}
				c.selectSlot(*next, 1, 0, 1)
			})
		}()
		return
	}
	c.selectSlot(s.card, n, -1, delta)
}
func (c *Controller) Close() {
	c.workMu.Lock()
	if c.closing {
		c.workMu.Unlock()
		<-c.done
		return
	}
	c.closing = true
	c.workMu.Unlock()
	close(c.stop)
	<-c.done
}
func (c *Controller) dispose() {
	c.saveCurrent()
	c.cancel()
	for _, s := range c.slots {
		s.cancel()
		if s.engine != nil {
			s.engine.Pause()
			_ = s.engine.Close()
		}
	}
	c.preloadVersion++
	_, _ = c.backend.UpdateFeedPreloadWindow(context.Background(), c.preloadVersion, "", "", nil)
	if c.floating != nil {
		_ = c.floating.Close()
		c.floating = nil
	}
	c.progressMu.Lock()
	c.progressClosing = true
	c.progressMu.Unlock()
	select {
	case c.progressWake <- struct{}{}:
	default:
	}
	<-c.progressDone
	c.backend.FlushPendingProgress()
	c.state.Mode = Idle
	c.state.Engine = nil
	c.system()
}
func (c *Controller) seek(s *slot, position float64) {
	if s.engine == nil || math.IsNaN(position) || math.IsInf(position, 0) {
		return
	}
	s.seekTarget = math.Max(0, position)
	s.seekPending = true
	s.seekAt = time.Now()
	s.engine.Seek(s.seekTarget)
	if s == c.current {
		c.state.DanmakuLaunchAfter = math.Max(0, s.seekTarget-.75)
		c.state.ClockEpoch++
		c.state.Status.Position = s.seekTarget
		c.state.Updated = time.Now()
	}
}
func (c *Controller) saveCurrent() {
	s := c.current
	if s == nil || s.dto == nil || s.engine == nil {
		return
	}
	status := s.engine.Snapshot()
	if !status.Ready && status.Position <= 0 && !(s.seekPending && s.seekTarget > 0) {
		return
	}
	status.Duration = math.Max(status.Duration, math.Max(float64(s.dto.DurationSec), c.state.Status.Duration))
	position := status.Position
	if s.seekPending {
		position = s.seekTarget
	}
	c.enqueueProgress(progressWrite{book: s.card, dto: *s.dto, position: position, duration: status.Duration, account: c.account})
	c.sequence++
	if c.hooks.Progress != nil {
		c.hooks.Progress(Progress{s.seriesID, s.number, position, c.state.Session, c.sequence})
	}
	c.lastSave = time.Now()
}
func (c *Controller) enqueueProgress(write progressWrite) {
	c.progressMu.Lock()
	last := len(c.progressQueue) - 1
	if last >= 0 && c.progressQueue[last].account == write.account && c.progressQueue[last].dto.EpisodeID == write.dto.EpisodeID {
		c.progressQueue[last] = write
	} else {
		c.progressQueue = append(c.progressQueue, write)
	}
	c.progressMu.Unlock()
	select {
	case c.progressWake <- struct{}{}:
	default:
	}
}
func (c *Controller) saveLoop() {
	defer close(c.progressDone)
	for range c.progressWake {
		for {
			c.progressMu.Lock()
			if len(c.progressQueue) == 0 {
				c.progressQueue = nil
				closing := c.progressClosing
				c.progressMu.Unlock()
				if closing {
					return
				}
				break
			}
			w := c.progressQueue[0]
			c.progressQueue = c.progressQueue[1:]
			c.progressMu.Unlock()
			if c.backend.PlaybackScope() == w.account {
				err := c.writeProgress(w)
				if err != nil {
					log.Printf("[native-player] save progress failed: %v", err)
					if c.backend.PlaybackScope() == w.account {
						c.post(func() {
							if c.account == w.account {
								c.state.Notice = progressSaveNotice(err)
							}
						})
					}
				}
			}
		}
	}
}

// FlushSnapshotForSessionEnd persists the latest published clock without making
// a native call. OS session-end callbacks run on AppKit's main thread and may
// not wait for the playback actor, which can itself be waiting for AppKit.
func (c *Controller) FlushSnapshotForSessionEnd() {
	s := c.Snapshot()
	if s.Mode == Idle || s.Playback == nil {
		return
	}
	position := s.Status.Position
	if s.Status.Playing && !s.Status.Buffering && !s.Paused && !s.Updated.IsZero() {
		position += math.Min(.25, math.Max(0, time.Since(s.Updated).Seconds())) * s.Preferences.Rate
	}
	if s.Status.Duration > 0 {
		position = math.Min(position, s.Status.Duration)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.backend.SaveProgressForScope(ctx, s.scope, s.Playback.SeriesID, s.Playback.EpisodeID, s.Book.Title, s.Book.CoverURL, s.Playback.EpisodeNum, s.Book.EpisodeCount, position, s.Status.Duration)
}

// AVFoundation requires a finite length for ordinary MP4 range responses. The
// progressive MSE endpoint intentionally has no final size while remuxing;
// HLS is the native cold path, and complete MP4 remains the instant cache path.
func nativeSourceURL(dto *ipc.PlaybackDTO) string {
	if !dto.IsFullCached && dto.HLSStreamURL != "" {
		return dto.HLSStreamURL
	}
	return dto.StreamURL
}

func (c *Controller) sourceTimeout(s *slot) time.Duration {
	if s.dto != nil && s.sourceURL == s.dto.FullStreamURL {
		return 45 * time.Second
	}
	return 7 * time.Second
}
func (c *Controller) loadFullForSeek(s *slot, target float64) {
	s.engine.Pause()
	if err := s.engine.Load(s.dto.FullStreamURL); err != nil {
		c.state.Error = "跳转准备失败，请重试"
		return
	}
	s.sourceURL = s.dto.FullStreamURL
	s.sourceStarted = time.Now()
	s.first = false
	s.fallback = 2
	c.state.Loading = true
	c.state.Error = ""
	c.seek(s, target)
	c.configure(s)
	if !c.state.Paused {
		s.engine.Play()
	}
}

func playerStatus(s *slot) avkit.Status {
	status := s.engine.Snapshot()
	if s.dto != nil {
		status.Duration = math.Max(status.Duration, float64(s.dto.DurationSec))
	}
	return status
}
