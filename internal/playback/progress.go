package playback

import (
	"context"
	"errors"
	"syscall"
	"time"
)

func storageErrorCode(err error) int {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return coded.Code() & 255 // Include SQLite's extended BUSY/LOCKED codes.
	}
	return 0
}

func progressSaveNotice(err error) string {
	if storageErrorCode(err) == 13 || errors.Is(err, syscall.ENOSPC) {
		return "磁盘空间不足，观看进度暂时无法保存"
	}
	return "观看进度暂时无法保存，请稍后重试"
}

func (c *Controller) writeProgress(w progressWrite) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for attempt := 0; ; attempt++ {
		if c.backend.PlaybackScope() != w.account {
			return nil
		}
		err := c.backend.SaveProgressForScope(ctx, w.account, w.dto.SeriesID, w.dto.EpisodeID, w.book.Title, w.book.CoverURL, w.dto.EpisodeNum, w.book.EpisodeCount, w.position, w.duration)
		code := storageErrorCode(err)
		if err == nil || attempt >= 2 || (code != 5 && code != 6) {
			return err
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}
