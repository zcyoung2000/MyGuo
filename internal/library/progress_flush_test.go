package library

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	userstore "myguo/internal/store"
)

func TestProgressFailureRemainsPendingAndBackoffIsBounded(t *testing.T) {
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 1001})
	}))
	item := HistoryItem{
		SeriesID: "710000000000001", Title: "Pending", EpisodeNum: 1,
		PositionSec: 12, DurationSec: 100, TotalEpisodes: 10, UpdatedAt: time.Now().Unix(),
	}
	if err := store.SaveHistory(item); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueHistoryUpload(item, "710000000000101"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.processPendingHistoryUploads(); err == nil {
		t.Fatal("processPendingHistoryUploads() error = nil, want failure")
	}
	if pending, err := store.PendingHistoryUploads(10); err != nil || len(pending) != 1 || pending[0].PositionSec != 12 {
		t.Fatalf("pending after failure = %#v, %v", pending, err)
	}
	requests := []userstore.PendingHistoryUpload{
		{HistoryItem: item, EpisodeID: "710000000000101"},
	}
	service.markHistoryUploadFailed(requests, context.Canceled)
	first := service.historyUploadRetries[historyUploadKey(requests[0])]
	service.markHistoryUploadFailed(requests, context.Canceled)
	second := service.historyUploadRetries[historyUploadKey(requests[0])]
	if !second.nextAt.After(first.nextAt) {
		t.Fatalf("backoff did not increase: first=%v second=%v", first.nextAt, second.nextAt)
	}
	delay := second.nextAt.Sub(time.Now())
	if delay > historyUploadMaxBackoff {
		t.Fatalf("backoff = %v, want <= %v", delay, historyUploadMaxBackoff)
	}
}

func TestFlushPendingProgressDrainsSavedRequestsBeforeClose(t *testing.T) {
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeJSON(w, map[string]any{"code": 0})
	}))
	for _, progress := range []struct {
		episodeID  string
		episodeNum int
		position   float64
	}{{"710000000000101", 1, 3}, {"710000000000102", 2, 4}} {
		if err := service.SaveProgress(context.Background(), "710000000000001", progress.episodeID, "剧", "", progress.episodeNum, 2, progress.position, 10); err != nil {
			t.Fatal(err)
		}
	}
	service.FlushPendingProgress()
	items, err := store.GetHistory()
	if err != nil || len(items) != 1 || items[0].EpisodeNum != 2 || items[0].PositionSec != 4 {
		t.Fatalf("history after flush = %#v, %v; want latest episode", items, err)
	}
	if pending, err := store.PendingHistoryUploads(10); err != nil || len(pending) != 0 {
		t.Fatalf("history uploads after flush = %#v, %v; want drained queue", pending, err)
	}
}

func TestExitProgressFlushKeepsTheLastWatchedEpisodeWhenRewatching(t *testing.T) {
	service, store := newSyncTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"code": 0}) }))
	if err := service.SaveProgress(context.Background(), "710000000000001", "710000000000102", "Replay", "", 2, 2, 50, 100); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveProgress(context.Background(), "710000000000001", "710000000000101", "Replay", "", 1, 2, 8, 100); err != nil {
		t.Fatal(err)
	}
	service.FlushPendingProgress()
	if item, ok := store.GetHistoryItem("710000000000001"); !ok || item.EpisodeNum != 1 || item.PositionSec != 8 {
		t.Fatalf("exit flush lost latest rewatch: %+v found=%t", item, ok)
	}
}
