package playback

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

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
