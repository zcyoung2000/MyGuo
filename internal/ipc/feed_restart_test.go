package ipc

import (
	"context"
	"testing"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
)

func TestRestartKeepsRecentExclusionsAfterManyCursorRotations(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	ctx := context.Background()
	svc := &HongguoService{store: s, media: &MediaServer{},
		discover: func(context.Context, hgapi.DiscoverOptions) (*hgapi.Page[model.SeriesSummary], error) {
			return &hgapi.Page[model.SeriesSummary]{Items: []model.SeriesSummary{{ID: "shown"}, {ID: "pending"}}, Cursor: "old", HasMore: true}, nil
		},
	}
	if _, err := svc.GetRecommendFeed(ctx, "推荐", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConsumeRecommendFeedItem(ctx, "推荐", "shown"); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := s.StartNextFeedGeneration("推荐"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc = &HongguoService{store: s, media: &MediaServer{},
		discover: func(context.Context, hgapi.DiscoverOptions) (*hgapi.Page[model.SeriesSummary], error) {
			return &hgapi.Page[model.SeriesSummary]{Items: []model.SeriesSummary{{ID: "shown"}, {ID: "pending"}, {ID: "fresh"}}, Cursor: "fresh-cursor", HasMore: true}, nil
		},
	}
	page, err := svc.GetRecommendFeed(ctx, "推荐", "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "fresh" {
		t.Fatalf("restart/session rotation repeated old items: %+v / %v", page, err)
	}
	restored, err := svc.RestoreRecommendFeed(ctx, "推荐")
	if err != nil || len(restored.Items) != 2 || restored.Items[0].ID != "pending" || restored.Items[1].ID != "fresh" || restored.Cursor != "fresh-cursor" {
		t.Fatalf("new page displaced the durable tail: %+v / %v", restored, err)
	}
}

func TestAllRepeatedRecommendPagesDoNotPublishRepeatsAsFallback(t *testing.T) {
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveFeedSeenIDs("推荐", 1, []string{"shown"}); err != nil {
		t.Fatal(err)
	}
	svc := &HongguoService{store: s, media: &MediaServer{},
		discover: func(context.Context, hgapi.DiscoverOptions) (*hgapi.Page[model.SeriesSummary], error) {
			return &hgapi.Page[model.SeriesSummary]{Items: []model.SeriesSummary{{ID: "shown"}}}, nil
		},
		resetDevice: func(context.Context) error { return nil },
	}
	page, err := svc.GetRecommendFeed(context.Background(), "推荐", "")
	if err != nil || len(page.Items) != 0 || !page.HasMore {
		t.Fatalf("repeated-only page should stay retryable without publishing old videos: %+v / %v", page, err)
	}
	queue, err := s.GetFeedQueue("推荐")
	if err != nil || len(queue) != 0 {
		t.Fatalf("repeated-only page reinserted old queue metadata: %+v / %v", queue, err)
	}
}
