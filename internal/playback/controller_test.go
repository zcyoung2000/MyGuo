package playback

import (
	"context"
	"errors"
	"fmt"
	"math"
	"myguo/internal/avkit"
	"myguo/internal/ipc"
	"myguo/internal/media"
	"strings"
	"sync"
	"testing"
	"time"
)

type testBackend struct {
	Backend
	mu         sync.Mutex
	scope      string
	saved      []progressWrite
	saveErr    func() error
	flushed    bool
	restore    *ipc.RecommendFeedResult
	feed       *ipc.RecommendFeedResult
	resolve    func(context.Context, string, int, string) (*ipc.PlaybackDTO, error)
	detail     func(context.Context, string) (*ipc.SeriesDetailDTO, error)
	fetch      func(context.Context, string, string) (*ipc.RecommendFeedResult, error)
	comments   func(context.Context, string, string, string) (*ipc.CommentPageDTO, error)
	danmaku    func(context.Context, string, string, int64, int64) (*ipc.DanmakuPage, error)
	nextSeason *ipc.SeriesCard
	preload    [][]media.FeedPreloadItem
	consumed   []string
	requests   []ipc.PlaybackDTO
}

func (b *testBackend) PlaybackScope() string { b.mu.Lock(); defer b.mu.Unlock(); return b.scope }
func (b *testBackend) GetProfile(context.Context) (ipc.UserProfile, error) {
	return ipc.UserProfile{UserID: "anonymous"}, nil
}
func (b *testBackend) PlayEpisode(ctx context.Context, id, episode string, num int, quality string, hls bool) (*ipc.PlaybackDTO, error) {
	b.mu.Lock()
	b.requests = append(b.requests, ipc.PlaybackDTO{SeriesID: id, EpisodeID: episode, EpisodeNum: num, Quality: quality})
	b.mu.Unlock()
	if b.resolve != nil {
		return b.resolve(ctx, id, num, quality)
	}
	return source(id, num, quality), nil
}

func TestQualitySwitchKeepsResolvedEpisodeInsteadOfBootstrappingCachedQuality(t *testing.T) {
	b := &testBackend{}
	c, _ := newControllerTest(t, b)
	c.Play("book", 1, 0, 1)
	waitSource(t, c, "book", 1)
	c.Command("quality", "720p")
	eventually(t, func() bool {
		s := c.Snapshot()
		return s.Playback != nil && s.Playback.Quality == "720p" && s.Status.Ready
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, request := range b.requests {
		if request.SeriesID == "book" && request.EpisodeNum == 1 && request.Quality == "720p" {
			if request.EpisodeID != "book-1" {
				t.Fatalf("quality switch bootstrapped with episode ID %q", request.EpisodeID)
			}
			return
		}
	}
	t.Fatal("quality switch did not negotiate its requested rendition")
}

func TestPreferredQualityCanBeSelectedAfterLowerQualityCacheBootstrap(t *testing.T) {
	b := &testBackend{}
	calls := 0
	b.resolve = func(_ context.Context, id string, n int, quality string) (*ipc.PlaybackDTO, error) {
		b.mu.Lock()
		if id == "book" && n == 1 {
			calls++
		}
		first := id == "book" && n == 1 && calls == 1
		b.mu.Unlock()
		if first {
			return source(id, n, "720p"), nil
		}
		return source(id, n, quality), nil
	}
	c, _ := newControllerTest(t, b)
	c.Play("book", 1, 0, 1)
	s := waitSource(t, c, "book", 1)
	if s.Preferences.Quality != "1080p" || s.Playback.Quality != "720p" {
		t.Fatal("fixture must bootstrap a lower-quality cache")
	}
	c.Command("quality", "1080p")
	eventually(t, func() bool {
		s := c.Snapshot()
		return s.Playback != nil && s.Playback.Quality == "1080p" && s.Status.Ready
	})
}
func source(id string, num int, quality string) *ipc.PlaybackDTO {
	return &ipc.PlaybackDTO{SeriesID: id, EpisodeID: fmt.Sprintf("%s-%d", id, num), EpisodeNum: num, StreamURL: fmt.Sprintf("%s/%d/%s.mp4", id, num, quality), HLSStreamURL: fmt.Sprintf("%s/%d/%s.m3u8", id, num, quality), FullStreamURL: "full:" + id, Quality: quality, LastPosition: 42, DurationSec: 200, Qualities: []ipc.QualityOptionDTO{{ID: "720p", Available: true}, {ID: "1080p", Available: true}}}
}
func (b *testBackend) GetSeriesDetail(ctx context.Context, id string) (*ipc.SeriesDetailDTO, error) {
	if b.detail != nil {
		return b.detail(ctx, id)
	}
	return &ipc.SeriesDetailDTO{ID: id, Title: "标题 " + id, EpisodeCount: 4, Episodes: []ipc.EpisodeDTO{{Number: 1, Playable: true}, {Number: 2, Playable: true}, {Number: 3, Playable: true}, {Number: 4, Playable: true}}}, nil
}
func (b *testBackend) GetNextSeason(context.Context, string) (*ipc.SeriesCard, error) {
	return b.nextSeason, nil
}
func (b *testBackend) RestoreRecommendFeed(context.Context, string) (*ipc.RecommendFeedResult, error) {
	if b.restore != nil {
		return b.restore, nil
	}
	return &ipc.RecommendFeedResult{HasMore: true}, nil
}
func (b *testBackend) GetRecommendFeed(ctx context.Context, channel, cursor string) (*ipc.RecommendFeedResult, error) {
	if b.fetch != nil {
		return b.fetch(ctx, channel, cursor)
	}
	if b.feed != nil {
		return b.feed, nil
	}
	return &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "D"}, {ID: "E"}, {ID: "F"}}}, nil
}
func (b *testBackend) ConsumeRecommendFeedItem(_ context.Context, channel, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consumed = append(b.consumed, channel+":"+id)
	return nil
}
func (b *testBackend) UpdateFeedPreloadWindow(_ context.Context, version int64, current, quality string, items []media.FeedPreloadItem) ([]media.FeedPreloadStatus, error) {
	b.mu.Lock()
	b.preload = append(b.preload, append([]media.FeedPreloadItem(nil), items...))
	b.mu.Unlock()
	var statuses []media.FeedPreloadStatus
	for _, item := range items {
		statuses = append(statuses, media.FeedPreloadStatus{EpisodeID: item.EpisodeID, Quality: item.Quality, CachedSeconds: 6, Active: true})
	}
	return statuses, nil
}
func (b *testBackend) SaveProgress(ctx context.Context, id, ep, title, cover string, num, total int, position, duration float64) error {
	return b.SaveProgressForScope(ctx, b.PlaybackScope(), id, ep, title, cover, num, total, position, duration)
}
func (b *testBackend) SaveProgressForScope(_ context.Context, scope, id, ep, title, cover string, num, total int, position, duration float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if scope != b.scope {
		return errors.New("account changed")
	}
	if b.saveErr != nil {
		if err := b.saveErr(); err != nil {
			return err
		}
	}
	b.saved = append(b.saved, progressWrite{account: scope, book: ipc.SeriesCard{ID: id, Title: title}, dto: ipc.PlaybackDTO{SeriesID: id, EpisodeID: ep, EpisodeNum: num}, position: position, duration: duration})
	return nil
}
func (b *testBackend) FlushPendingProgress()                            { b.mu.Lock(); b.flushed = true; b.mu.Unlock() }
func (b *testBackend) IsFavorite(context.Context, string) (bool, error) { return false, nil }
func (b *testBackend) GetEpisodeComments(ctx context.Context, id, ep, cursor string) (*ipc.CommentPageDTO, error) {
	if b.comments != nil {
		return b.comments(ctx, id, ep, cursor)
	}
	return &ipc.CommentPageDTO{}, nil
}
func (b *testBackend) GetDanmaku(ctx context.Context, id, ep string, start, duration int64) (*ipc.DanmakuPage, error) {
	if b.danmaku != nil {
		return b.danmaku(ctx, id, ep, start, duration)
	}
	return &ipc.DanmakuPage{}, nil
}

type testEngine struct {
	mu           sync.Mutex
	status       avkit.Status
	url          string
	rate, volume float64
	muted        bool
	closed       bool
	plays        int
	loads        []string
	seeks        []float64
	warmNetwork  bool
	holdClock    bool
}

func (e *testEngine) NativeView() uintptr  { return 0 }
func (e *testEngine) CaptureFrame() []byte { return []byte("transition-frame") }
func (e *testEngine) Load(url string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.url = url
	e.loads = append(e.loads, url)
	e.status = avkit.Status{Ready: true, Displayed: true, Duration: 200, Buffered: 200, PictureInPicture: e.status.PictureInPicture}
	return nil
}
func (e *testEngine) Play() { e.mu.Lock(); e.status.Playing = true; e.plays++; e.mu.Unlock() }
func (e *testEngine) Pause() {
	e.mu.Lock()
	e.status.Playing = false
	e.warmNetwork = false
	e.mu.Unlock()
}
func (e *testEngine) Seek(position float64) {
	e.mu.Lock()
	e.status.Position = position
	e.seeks = append(e.seeks, position)
	e.status.Ended = false
	e.mu.Unlock()
}
func (e *testEngine) Configure(rate, volume float64, muted bool, fit string) {
	e.mu.Lock()
	e.rate = rate
	e.volume = volume
	e.muted = muted
	e.mu.Unlock()
}
func (e *testEngine) Preroll() { e.mu.Lock(); e.warmNetwork = true; e.mu.Unlock() }
func (e *testEngine) Snapshot() avkit.Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Successful mock playback advances off the first frame. A separate held
	// clock fixture exercises a decoded but stalled native pipeline.
	if e.status.Playing && e.status.Position == 0 && !e.holdClock {
		e.status.Position = .1
	}
	return e.status
}
func (e *testEngine) TogglePictureInPicture(context.Context) error {
	e.mu.Lock()
	e.status.PictureInPicture = !e.status.PictureInPicture
	e.mu.Unlock()
	return nil
}
func (e *testEngine) Close() error {
	e.mu.Lock()
	e.closed = true
	e.status.Playing = false
	e.mu.Unlock()
	return nil
}

type engineRegistry struct {
	mu      sync.Mutex
	engines []*testEngine
}

func (r *engineRegistry) make() (avkit.Player, error) {
	e := &testEngine{}
	r.mu.Lock()
	r.engines = append(r.engines, e)
	r.mu.Unlock()
	return e, nil
}
func (r *engineRegistry) active() (open, audible int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.engines {
		e.mu.Lock()
		if !e.closed {
			open++
			if e.status.Playing && !e.muted {
				audible++
			}
		}
		e.mu.Unlock()
	}
	return
}
func newControllerTest(t *testing.T, b *testBackend) (*Controller, *engineRegistry) {
	t.Helper()
	r := &engineRegistry{}
	c := New(b, r.make, "", Hooks{})
	t.Cleanup(c.Close)
	return c, r
}
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("controller did not reach expected state")
}
func waitSource(t *testing.T, c *Controller, id string, num int) State {
	t.Helper()
	eventually(t, func() bool {
		s := c.Snapshot()
		return s.Playback != nil && s.Playback.SeriesID == id && s.Playback.EpisodeNum == num && s.Status.Ready
	})
	return c.Snapshot()
}
func barrier(t *testing.T, c *Controller) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLateSceneCannotReplaceNewVideoOrPersistItsClock(t *testing.T) {
	release := make(chan struct{})
	requested := make(chan struct{})
	b := &testBackend{resolve: func(ctx context.Context, id string, num int, q string) (*ipc.PlaybackDTO, error) {
		if id == "old" && num == 1 {
			close(requested)
			<-release
		}
		return source(id, num, q), nil
	}}
	c, _ := newControllerTest(t, b)
	c.Play("old", 1, 0, 1)
	<-requested
	c.Play("new", 2, 12, 2)
	waitSource(t, c, "new", 2)
	close(release)
	barrier(t, c)
	c.Command("pause", nil)
	barrier(t, c)
	c.Close()
	if s := c.Snapshot(); s.Playback.SeriesID != "new" {
		t.Fatal("late old video replaced new selection")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, write := range b.saved {
		if write.dto.SeriesID != "new" {
			t.Fatalf("stale progress saved: %+v", write)
		}
	}
}
func TestSelectionPreservesExplicitResumePauseSpeedVolumeAndQuality(t *testing.T) {
	c, r := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	if s.Status.Position > .1 {
		t.Fatalf("explicit restart used saved position: %g", s.Status.Position)
	}
	c.Command("pause", nil)
	c.Command("speed", 1.5)
	c.Command("volume", .4)
	c.Command("seek", 87.5)
	c.Command("quality", "720p")
	eventually(t, func() bool {
		s := c.Snapshot()
		return s.Playback != nil && s.Playback.Quality == "720p" && s.Status.Ready
	})
	s = c.Snapshot()
	if !s.Paused || s.Status.Position != 87.5 || s.Preferences.Rate != 1.5 || s.Preferences.Volume != .4 {
		t.Fatalf("quality switch lost user intent: %+v", s)
	}
	c.Command("nextEpisode", nil)
	s = waitSource(t, c, "A", 2)
	if !s.Paused {
		t.Fatal("paused episode change resumed playback")
	}
	if _, audible := r.active(); audible != 0 {
		t.Fatalf("paused scene has %d audible players", audible)
	}
}
func TestNormalPlayUsesSavedPositionAndNextSeason(t *testing.T) {
	b := &testBackend{nextSeason: &ipc.SeriesCard{ID: "season2", EpisodeCount: 4}}
	c, _ := newControllerTest(t, b)
	c.Play("season1", 4, -1, 11)
	s := waitSource(t, c, "season1", 4)
	if s.Status.Position != 42 {
		t.Fatalf("saved resume lost: %g", s.Status.Position)
	}
	c.Command("nextEpisode", nil)
	s = waitSource(t, c, "season2", 1)
	if s.Status.Position > .1 || s.Session != 11 {
		t.Fatal("next season lost session or started at stale position")
	}
}
func TestFeedReverseSwitchKeepsAtMostThreeResidentAndOneAudible(t *testing.T) {
	c, r := newControllerTest(t, &testBackend{})
	c.ShowFeed("漫剧")
	waitSource(t, c, "A", 1)
	for _, id := range []string{"B", "C", "B", "A", "B", "C", "D"} {
		s := c.Snapshot()
		delta := "next"
		if id < s.Book.ID {
			delta = "prev"
		}
		c.Command(delta, nil)
		waitSource(t, c, id, 1)
		open, audible := r.active()
		if len(c.Snapshot().TransitionFrame) == 0 {
			t.Fatal("feed switch lost outgoing frame")
		}
		if open > 3 || audible != 1 {
			t.Fatalf("selection %s: open=%d audible=%d", id, open, audible)
		}
	}
}
func TestRestoredFeedUsesPersistedCursorWithoutRepeatingTheFirstPage(t *testing.T) {
	fetched := make(chan string, 2)
	b := &testBackend{restore: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "saved"}}, Cursor: "persisted", HasMore: true}, fetch: func(ctx context.Context, channel, cursor string) (*ipc.RecommendFeedResult, error) {
		fetched <- cursor
		return &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "fresh"}}}, nil
	}}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("")
	waitSource(t, c, "saved", 1)
	select {
	case cursor := <-fetched:
		if cursor != "persisted" {
			t.Fatalf("restarted feed pagination at %q", cursor)
		}
	case <-time.After(time.Second):
		t.Fatal("feed not fetched")
	}
}
func TestPendingFeedNavigationIsAppliedWhenThePageArrives(t *testing.T) {
	release := make(chan struct{})
	b := &testBackend{restore: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "A"}}, Cursor: "next", HasMore: true}, fetch: func(ctx context.Context, channel, cursor string) (*ipc.RecommendFeedResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "B"}}}, nil
	}}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("")
	waitSource(t, c, "A", 1)
	c.Command("next", nil)
	barrier(t, c)
	close(release)
	waitSource(t, c, "B", 1)
}
func TestAccountSwitchStopsTheOldClockAndRejectsQueuedWrites(t *testing.T) {
	b := &testBackend{scope: "user:A"}
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 10, 1)
	waitSource(t, c, "A", 1)
	b.mu.Lock()
	b.scope = "user:B"
	b.mu.Unlock()
	c.Command("seek", 95)
	eventually(t, func() bool { return c.Snapshot().Mode == Idle })
	c.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, write := range b.saved {
		if write.account == "user:B" {
			t.Fatalf("old playback inserted in new account: %+v", write)
		}
	}
}
func TestCloseSavesLastClockBeforeFlushingAndReleasesAllDecoders(t *testing.T) {
	b := &testBackend{}
	c, r := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	s := waitSource(t, c, "A", 1)
	s.Engine.Seek(112.25)
	c.Close()
	open, audible := r.active()
	if open != 0 || audible != 0 {
		t.Fatal("close retained a media decoder")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.flushed || len(b.saved) == 0 || b.saved[len(b.saved)-1].position != 112.25 {
		t.Fatalf("close did not flush final position: %+v", b.saved)
	}
}
func TestInvalidSeekAndVolumeCannotPoisonTheMediaClock(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 10, 1)
	waitSource(t, c, "A", 1)
	c.Command("seek", math.NaN())
	c.Command("seekBy", math.Inf(1))
	c.Command("volume", math.NaN())
	barrier(t, c)
	s := c.Snapshot()
	if math.IsNaN(s.Status.Position) || math.IsInf(s.Status.Position, 0) || math.IsNaN(s.Preferences.Volume) {
		t.Fatal("nonfinite values reached playback")
	}
}
func TestStaleCommentsCannotAppearInTheNextEpisode(t *testing.T) {
	release := make(chan struct{})
	requested := make(chan struct{}, 1)
	b := &testBackend{comments: func(ctx context.Context, id, ep, cursor string) (*ipc.CommentPageDTO, error) {
		if ep == "A-1" {
			requested <- struct{}{}
			<-release
		}
		return &ipc.CommentPageDTO{Items: []ipc.CommentDTO{{ID: ep, Content: ep}}}, nil
	}}
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	c.Command("panel", "comments")
	<-requested
	c.Command("nextEpisode", nil)
	waitSource(t, c, "A", 2)
	close(release)
	eventually(t, func() bool { s := c.Snapshot(); return len(s.CommentItems) > 0 })
	for _, comment := range c.Snapshot().CommentItems {
		if comment.ID != "A-2" {
			t.Fatalf("late old comments rendered: %+v", comment)
		}
	}
}
func TestPiPSwitchUsesTheSameNativeSurfaceAndClock(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.ShowFeed("")
	s := waitSource(t, c, "A", 1)
	_ = s.Engine.TogglePictureInPicture(context.Background())
	c.Command("next", nil)
	next := waitSource(t, c, "B", 1)
	if next.Engine != s.Engine || !next.Engine.Snapshot().PictureInPicture {
		t.Fatal("PiP retained the old video instead of following selection")
	}
}

type prefixEngine struct{ testEngine }

func (e *prefixEngine) Load(url string) error {
	if err := e.testEngine.Load(url); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !strings.HasPrefix(url, "full:") {
		e.status.Duration = 6
		e.status.Seekable = 6
	} else {
		e.status.Seekable = 200
	}
	return nil
}
func TestSeekPastGrowingHLSPrefixUsesFullSourceWithoutLosingPauseOrDuration(t *testing.T) {
	b := &testBackend{}
	c := New(b, func() (avkit.Player, error) { return &prefixEngine{}, nil }, "", Hooks{})
	defer c.Close()
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	c.Command("pause", nil)
	barrier(t, c)
	if c.Snapshot().Status.Duration != 200 {
		t.Fatal("pause replaced metadata duration with the six-second prefix")
	}
	c.Command("seek", 137)
	eventually(t, func() bool {
		s := c.Snapshot()
		if s.Engine == nil {
			return false
		}
		e := s.Engine.(*prefixEngine)
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.url == "full:A" && e.status.Position == 137
	})
	s := waitSource(t, c, "A", 1)
	if !s.Paused || s.Status.Playing || s.Status.Duration != 200 {
		t.Fatalf("seek lost intent: %+v", s.Status)
	}
	c.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.saved) == 0 || b.saved[len(b.saved)-1].position != 137 || b.saved[len(b.saved)-1].duration != 200 {
		t.Fatalf("partial-HLS clock persisted incorrectly: %+v", b.saved)
	}
}
func TestInitialResumeOutsideGrowingPrefixUsesFullSource(t *testing.T) {
	c := New(&testBackend{}, func() (avkit.Player, error) { return &prefixEngine{}, nil }, "", Hooks{})
	defer c.Close()
	c.Play("A", 1, 42, 1)
	s := waitSource(t, c, "A", 1)
	e := s.Engine.(*prefixEngine)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.url != "full:A" || e.status.Position != 42 || !e.status.Playing {
		t.Fatalf("resume stuck in HLS prefix: %q %+v", e.url, e.status)
	}
}
func TestPausedUnchangedClockDoesNotContinuouslyInvalidateUI(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 1, 10, 1)
	waitSource(t, c, "A", 1)
	c.Command("pause", nil)
	barrier(t, c)
	// Drain metadata/preload acknowledgments before measuring the quiet scene.
	time.Sleep(150 * time.Millisecond)
	revision := c.Snapshot().Revision
	time.Sleep(650 * time.Millisecond)
	if c.Snapshot().Revision != revision {
		t.Fatalf("unchanged paused view rebuilt: %d -> %d", revision, c.Snapshot().Revision)
	}
}
func TestCommentCursorCyclesStopAndDeduplicate(t *testing.T) {
	b := &testBackend{comments: func(ctx context.Context, id, ep, cursor string) (*ipc.CommentPageDTO, error) {
		next := "A"
		if cursor == "A" {
			next = "B"
		}
		return &ipc.CommentPageDTO{HasMore: true, NextCursor: next, Items: []ipc.CommentDTO{{ID: "same", Content: "one"}}}, nil
	}}
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	c.Command("panel", "comments")
	eventually(t, func() bool { return c.Snapshot().CommentHasMore })
	c.MoreComments()
	eventually(t, func() bool {
		return c.Snapshot().CommentHasMore && !c.Snapshot().CommentLoading && c.commentCursorForTest() == "B"
	})
	c.MoreComments()
	eventually(t, func() bool { return !c.Snapshot().CommentHasMore && !c.Snapshot().CommentLoading })
	if len(c.Snapshot().CommentItems) != 1 {
		t.Fatal("duplicate comments appended")
	}
}
func (c *Controller) commentCursorForTest() string {
	result := make(chan string, 1)
	if !c.post(func() { result <- c.commentCursor }) {
		return ""
	}
	return <-result
}

func TestReplayAfterFinalEpisodeStartsAtZero(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.Play("A", 4, 0, 1)
	s := waitSource(t, c, "A", 4)
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Position = 200
	e.status.Ended = true
	e.status.Playing = false
	e.mu.Unlock()
	eventually(t, func() bool { return c.Snapshot().Paused && c.Snapshot().Status.Ended })
	c.Command("toggle", nil)
	barrier(t, c)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status.Position != 0 || !e.status.Playing {
		t.Fatalf("replay remained ended: %+v", e.status)
	}
}
func TestFeedFinishingFinalEpisodeContinuesWithNextDrama(t *testing.T) {
	c, _ := newControllerTest(t, &testBackend{})
	c.ShowFeed("")
	waitSource(t, c, "A", 1)
	c.Command("episode", 4)
	s := waitSource(t, c, "A", 4)
	e := s.Engine.(*testEngine)
	e.mu.Lock()
	e.status.Position = e.status.Duration
	e.status.Ended = true
	e.status.Playing = false
	e.mu.Unlock()
	waitSource(t, c, "B", 1)
}

func TestExplicitRefreshBypassesRestoredQueue(t *testing.T) {
	b := &testBackend{restore: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "saved"}}, HasMore: false}, feed: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "fresh"}}}}
	c, _ := newControllerTest(t, b)
	c.ShowFeed("漫剧")
	waitSource(t, c, "saved", 1)
	c.Command("refresh", nil)
	s := waitSource(t, c, "fresh", 1)
	if s.Channel != "漫剧" {
		t.Fatal("refresh lost channel")
	}
}

func TestUnplayedFeedStaysInPersistentQueueUntilItsFirstFrame(t *testing.T) {
	b := &testBackend{}
	c := New(b, func() (avkit.Player, error) { return &notReadyEngine{}, nil }, "", Hooks{})
	defer c.Close()
	c.ShowFeed("")
	eventually(t, func() bool { return c.Snapshot().Playback != nil })
	b.mu.Lock()
	early := len(b.consumed)
	b.mu.Unlock()
	if early != 0 {
		t.Fatal("unplayed feed was consumed before a frame")
	}
	e := c.Snapshot().Engine.(*notReadyEngine)
	e.mu.Lock()
	e.status.Ready = true
	e.status.Displayed = true
	e.mu.Unlock()
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.consumed) == 1 })
}

type notReadyEngine struct{ testEngine }

func (e *notReadyEngine) Load(url string) error {
	if err := e.testEngine.Load(url); err != nil {
		return err
	}
	e.mu.Lock()
	e.status.Ready = false
	e.status.Displayed = false
	e.mu.Unlock()
	return nil
}
