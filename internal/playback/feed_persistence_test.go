package playback

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myguo/internal/avkit"
	"myguo/internal/ipc"
	"myguo/internal/store"
)

type feedPersistenceBackend struct {
	*testBackend
	consume func(context.Context, string, string) error
	restore func(context.Context, string) (*ipc.RecommendFeedResult, error)
}

func (b *feedPersistenceBackend) ConsumeRecommendFeedItem(ctx context.Context, channel, id string) error {
	return b.consume(ctx, channel, id)
}
func (b *feedPersistenceBackend) RestoreRecommendFeed(ctx context.Context, channel string) (*ipc.RecommendFeedResult, error) {
	if b.restore != nil {
		return b.restore(ctx, channel)
	}
	return b.testBackend.RestoreRecommendFeed(ctx, channel)
}

func TestFeedReadinessWithoutDisplayedFrameIsNotConsumedAndFailedWriteRetries(t *testing.T) {
	var calls atomic.Int32
	b := &feedPersistenceBackend{testBackend: &testBackend{feed: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "A"}}}}}
	b.consume = func(context.Context, string, string) error {
		if calls.Add(1) <= 2 {
			return errors.New("temporary storage failure")
		}
		return nil
	}
	c := New(b, func() (avkit.Player, error) { return &notReadyEngine{}, nil }, "", Hooks{})
	defer c.Close()
	c.ShowFeed("")
	eventually(t, func() bool { return c.Snapshot().Engine != nil })
	e := c.Snapshot().Engine.(*notReadyEngine)
	e.mu.Lock()
	e.status.Ready = true
	e.status.Displayed = false
	e.mu.Unlock()
	eventually(t, func() bool { return c.Snapshot().Status.Ready })
	if calls.Load() != 0 {
		t.Fatal("a ready decoder without a displayed frame consumed the feed")
	}
	e.mu.Lock()
	e.status.Displayed = true
	e.mu.Unlock()
	eventually(t, func() bool { return calls.Load() == 3 })
	c.Close()
	if calls.Load() != 3 {
		t.Fatal("shutdown duplicated a successful acknowledgement")
	}
}

func TestCloseDrainsFeedWriteAndRestartBeginsAtUnviewedTail(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	dir := t.TempDir()
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	for _, id := range []string{"A", "B"} {
		payload, _ := json.Marshal(ipc.SeriesCard{ID: id})
		if err := s.AppendFeedQueue("推荐", []store.FeedQueueEntry{{SeriesID: id, Payload: payload}}); err != nil {
			t.Fatal(err)
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	b := &feedPersistenceBackend{testBackend: &testBackend{feed: &ipc.RecommendFeedResult{}}}
	b.restore = func(context.Context, string) (*ipc.RecommendFeedResult, error) {
		rows, err := s.GetFeedQueue("推荐")
		page := &ipc.RecommendFeedResult{}
		for _, row := range rows {
			page.Items = append(page.Items, ipc.SeriesCard{ID: row.SeriesID})
		}
		return page, err
	}
	b.consume = func(ctx context.Context, channel, id string) error {
		close(entered)
		select {
		case <-release:
			return s.ConsumeFeedQueueContext(ctx, channel, id)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c := New(b, func() (avkit.Player, error) { return &notReadyEngine{}, nil }, "", Hooks{})
	defer c.Close()
	defer unblock()
	c.ShowFeed("")
	eventually(t, func() bool { return c.Snapshot().Engine != nil })
	e := c.Snapshot().Engine.(*notReadyEngine)
	e.mu.Lock()
	e.status.Ready, e.status.Displayed = true, true
	e.mu.Unlock()
	// The final engine snapshot must persist a frame even if the actor has
	// not published its next tick; both paths must drain before Close returns.
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("quit did not acknowledge the final displayed frame")
	}
	select {
	case <-closed:
		t.Fatal("quit returned while feed storage was blocked")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("quit did not finish after the acknowledgement committed")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetFeedQueue("推荐")
	if err != nil || len(rows) != 1 || rows[0].SeriesID != "B" {
		t.Fatalf("restart repeated the displayed card: %+v / %v", rows, err)
	}
	seen, err := s.GetRecentFeedSeenIDs("推荐")
	if err != nil || !seen["A"] {
		t.Fatalf("displayed card was not durably excluded: %v", err)
	}
	// Recreate the controller as well as the SQLite connection.
	next := New(b, func() (avkit.Player, error) { return &notReadyEngine{}, nil }, "", Hooks{})
	defer next.Close()
	next.ShowFeed("")
	eventually(t, func() bool { return next.Snapshot().Playback != nil })
	if next.Snapshot().Playback.SeriesID != "B" {
		t.Fatal("restarted recommendation controller did not resume the tail")
	}
}

func TestFailedFeedWriteFromRetiredSceneIsRetriedOnQuit(t *testing.T) {
	var calls atomic.Int32
	b := &feedPersistenceBackend{testBackend: &testBackend{feed: &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "A"}}}}}
	b.consume = func(ctx context.Context, _, _ string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if calls.Add(1) <= 3 {
			return errors.New("temporary write failure")
		}
		return nil
	}
	c := New(b, func() (avkit.Player, error) { return &testEngine{}, nil }, "", Hooks{})
	defer c.Close()
	c.ShowFeed("")
	eventually(t, func() bool { return c.Snapshot().Notice == feedSaveNotice })
	c.Stop()
	barrier(t, c)
	c.Close()
	if calls.Load() != 4 {
		t.Fatalf("quit lost a failed acknowledgement after scene retirement: calls=%d", calls.Load())
	}
}
