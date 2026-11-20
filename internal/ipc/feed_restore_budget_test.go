package ipc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"myguo/internal/hgapi/model"
)

func TestRestoringLargeQueueOnlyValidatesImmediateLookaheadBeforePlayback(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := &HongguoService{store: store, media: &MediaServer{}}
	cards := make([]SeriesCard, 18)
	for i := range cards {
		cards[i] = SeriesCard{ID: fmt.Sprint(i), EpisodeCount: 1000}
	}
	if err := svc.queueRecommendCards("推荐", cards); err != nil {
		t.Fatal(err)
	}
	requests := 0
	svc.seriesBatch = func(ctx context.Context, ids []string) ([]model.SeriesDetail, error) {
		requests++
		if len(ids) != 3 || ids[0] != "0" || ids[2] != "2" {
			t.Fatalf("restore blocked on its whole tail: %v", ids)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("restore inherited the full background catalog budget")
		}
		return nil, nil // Inconclusive metadata preserves the pending queue.
	}
	page, err := svc.RestoreRecommendFeed(context.Background(), "推荐")
	if err != nil || len(page.Items) != 18 || page.Items[17].ID != "17" || requests != 1 {
		t.Fatalf("restore lost its tail or repeated validation: %+v / %v / %d", page, err, requests)
	}
}
