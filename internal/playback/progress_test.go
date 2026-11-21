package playback

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"myguo/internal/ipc"
)

func TestPartialPlaybackMarksEpisodeWatchedAcrossEpisodeChanges(t *testing.T) {
	shared := &ipc.SeriesDetailDTO{ID: "book", EpisodeCount: 4, Episodes: []ipc.EpisodeDTO{
		{Number: 1, Playable: true}, {Number: 2, Playable: true},
		{Number: 3, Playable: true}, {Number: 4, Playable: true, Watched: true},
	}}
	b := &testBackend{detail: func(context.Context, string) (*ipc.SeriesDetailDTO, error) { return shared, nil }}
	c, _ := newControllerTest(t, b)
	c.Play("book", 1, 0, 1)
	waitSource(t, c, "book", 1)
	eventually(t, func() bool {
		s := c.Snapshot()
		return len(s.Episodes) == 4 && s.Episodes[0].Watched && s.Episodes[3].Watched
	})
	before := c.Snapshot()
	c.Command("episode", 2)
	waitSource(t, c, "book", 2)
	eventually(t, func() bool {
		s := c.Snapshot()
		return len(s.Episodes) == 4 && s.Episodes[0].Watched && s.Episodes[1].Watched
	})
	after := c.Snapshot()
	if after.Episodes[2].Watched || !after.Episodes[3].Watched {
		t.Fatalf("skipped or remote episode state changed: %+v", after.Episodes)
	}
	if before.Episodes[1].Watched || shared.Episodes[0].Watched || shared.Episodes[1].Watched {
		t.Fatal("watch updates mutated a published snapshot or backend metadata")
	}
}

func TestDelayedEpisodeMetadataKeepsObservedPlayback(t *testing.T) {
	release := make(chan struct{})
	b := &testBackend{detail: func(ctx context.Context, id string) (*ipc.SeriesDetailDTO, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return &ipc.SeriesDetailDTO{ID: id, EpisodeCount: 2, Episodes: []ipc.EpisodeDTO{{Number: 1, Playable: true}, {Number: 2, Playable: true}}}, nil
		}
	}}
	c, _ := newControllerTest(t, b)
	c.Play("book", 1, 0, 1)
	waitSource(t, c, "book", 1)
	eventually(t, func() bool { return c.Snapshot().Status.Position > 0 })
	close(release)
	eventually(t, func() bool {
		s := c.Snapshot()
		return len(s.Episodes) == 2 && s.Episodes[0].Watched && !s.Episodes[1].Watched
	})
}

type storageTestError int

func (e storageTestError) Error() string { return "storage fixture" }
func (e storageTestError) Code() int     { return int(e) }

func TestTransientProgressWriteRetriesWithoutMisleadingNotice(t *testing.T) {
	var attempts atomic.Int32
	b := &testBackend{saveErr: func() error {
		if attempts.Add(1) == 1 {
			return fmt.Errorf("wrapped: %w", storageTestError(517))
		}
		return nil
	}}
	c, _ := newControllerTest(t, b)
	c.Play("A", 1, 0, 1)
	waitSource(t, c, "A", 1)
	c.Command("pause", nil)
	eventually(t, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return len(b.saved) > 0 })
	if attempts.Load() < 2 || c.Snapshot().Notice != "" {
		t.Fatal("transient contention lost progress or showed a false disk warning")
	}
}

func TestStorageNoticeOnlyBlamesDiskWhenFull(t *testing.T) {
	for _, err := range []error{storageTestError(5), errors.New("permission denied")} {
		if strings.Contains(progressSaveNotice(err), "磁盘空间") {
			t.Fatal("non-disk failure blamed on disk space")
		}
	}
	for _, err := range []error{storageTestError(13), fmt.Errorf("write: %w", syscall.ENOSPC)} {
		if !strings.Contains(progressSaveNotice(err), "磁盘空间不足") {
			t.Fatal("disk-full error lost useful message")
		}
	}
}
