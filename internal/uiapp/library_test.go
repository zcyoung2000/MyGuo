package uiapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"myguo/internal/ipc"
)

type fakeLibrary struct {
	history      []ipc.HistoryItem
	favorites    []ipc.FavoriteItem
	reservations []ipc.ReservationItem
	likes        []ipc.LikeItem
	err          error
}

func (f *fakeLibrary) GetHistory(context.Context) ([]ipc.HistoryItem, error) {
	return f.history, f.err
}
func (f *fakeLibrary) GetFavorites(context.Context) ([]ipc.FavoriteItem, error) {
	return f.favorites, f.err
}
func (f *fakeLibrary) GetReservations(context.Context) ([]ipc.ReservationItem, error) {
	return f.reservations, f.err
}
func (f *fakeLibrary) GetLikes(context.Context) ([]ipc.LikeItem, error) {
	return f.likes, f.err
}

type partialHistoryLibrary struct {
	fakeLibrary
	items []ipc.HistoryItem
	err   error
}

func (f *partialHistoryLibrary) RefreshHistory(context.Context) ([]ipc.HistoryItem, error) {
	return f.items, f.err
}
func (f *partialHistoryLibrary) RefreshFavorites(ctx context.Context) ([]ipc.FavoriteItem, error) {
	return f.GetFavorites(ctx)
}
func (f *partialHistoryLibrary) RefreshLikes(ctx context.Context) ([]ipc.LikeItem, error) {
	return f.GetLikes(ctx)
}
func (f *partialHistoryLibrary) RefreshReservations(ctx context.Context) ([]ipc.ReservationItem, error) {
	return f.GetReservations(ctx)
}

func TestLoadHistoryMapsOnlyRealLocalRecords(t *testing.T) {
	provider := &fakeLibrary{history: []ipc.HistoryItem{{
		SeriesID: "123456", Title: "真实历史", CoverURL: "cover", EpisodeNum: 4,
		PositionSec: 42, DurationSec: 120, TotalEpisodes: 10, UpdatedAt: time.Now().Unix(),
	}}}
	app := NewTestApp()
	app.libraryData = provider
	app.SetInvalidator(func() {})
	app.loadLibrary(libraryHistory)
	if app.library.History.Loading || app.library.History.Error != "" || len(app.library.History.Items) != 1 {
		t.Fatalf("history state = %+v", app.library.History)
	}
	item := app.library.History.Items[0]
	if item.ID != "123456" || item.Title != "真实历史" || item.ResumeEpisode != 4 || item.Progress != 35 || item.Rank != 0 {
		t.Fatalf("history item = %+v", item)
	}
}

func TestLoadFavoritesAndReservationsUseProviderData(t *testing.T) {
	provider := &fakeLibrary{
		favorites:    []ipc.FavoriteItem{{SeriesID: "223456", Title: "收藏剧", CoverURL: "cover", TotalEpisodes: 9}},
		reservations: []ipc.ReservationItem{{SeriesID: "323456", Title: "预约剧", Note: "待上线"}},
		likes:        []ipc.LikeItem{{SeriesID: "423456", Title: "点赞剧集", EpisodeNum: 3}},
	}
	app := NewTestApp()
	app.libraryData = provider
	app.SetInvalidator(func() {})
	app.loadLibrary(libraryFavorites)
	if len(app.library.Favorites.Items) != 1 || app.library.Favorites.Items[0].Title != "收藏剧" {
		t.Fatalf("favorites = %+v", app.library.Favorites)
	}
	app.loadLibrary(libraryReservations)
	if len(app.library.Reserved.Items) != 1 || app.library.Reserved.Items[0].Title != "预约剧" {
		t.Fatalf("reservations = %+v", app.library.Reserved)
	}
	app.loadLibrary(libraryLikes)
	if len(app.library.Likes.Items) != 1 || app.library.Likes.Items[0].Title != "点赞剧集" {
		t.Fatalf("likes = %+v", app.library.Likes)
	}
}

func TestLoadLibraryShowsErrorWithoutSyntheticRecords(t *testing.T) {
	provider := &fakeLibrary{err: errors.New("sqlite unavailable")}
	app := NewTestApp()
	app.libraryData = provider
	app.SetInvalidator(func() {})
	app.loadLibrary(libraryHistory)
	if app.library.History.Loading || app.library.History.Error == "" || len(app.library.History.Items) != 0 {
		t.Fatalf("error history = %+v", app.library.History)
	}
}

func TestLoadHistoryKeepsPartialRowsAlongsideSyncError(t *testing.T) {
	provider := &partialHistoryLibrary{
		items: []ipc.HistoryItem{{SeriesID: "710000000000001", Title: "远端已读到", EpisodeNum: 3, TotalEpisodes: 10}},
		err:   errors.New("page two timed out"),
	}
	app := NewTestApp()
	app.libraryData = provider
	app.SetInvalidator(func() {})
	app.loadLibraryWithRefresh(libraryHistory, true)
	state := app.LibraryState().History
	if state.Loading || !state.Loaded || state.Empty || state.Error == "" || len(state.Items) != 1 || state.Items[0].Title != "远端已读到" {
		t.Fatalf("partial history state = %+v", state)
	}
}

func TestHistoryCardsNameMissingArtworkAndTitle(t *testing.T) {
	const id = "710000000000001"
	app := NewTestApp()
	app.view = "history"
	app.artwork = newArtworkLoader("", nil, nil)
	app.artwork.entries["https://media.invalid/missing.jpg"] = artworkEntry{retryAt: time.Now().Add(time.Hour)}
	app.library.History = Library{Loaded: true, Items: []CatalogItem{
		{ID: id, Title: "剧名待补全 · " + id, CoverURL: ""},
		{ID: "710000000000002", Title: "另一部", CoverURL: "https://media.invalid/missing.jpg"},
	}}
	tt := ui.NewTester(app.Render, 1200, 800)
	tt.Frame()
	for _, label := range []string{"剧名待补全 · " + id, "封面待补全", "封面加载失败"} {
		if !tt.HasText(label) {
			t.Fatalf("history card status %q missing; texts=%v", label, tt.Texts())
		}
	}
}

func TestHistoryPartialSyncErrorLeavesCardsVisible(t *testing.T) {
	app := NewTestApp()
	app.view = "history"
	app.library.History = Library{
		Title: "观看历史", Loaded: true,
		Error: "云端历史读取未完成，已读取和本机记录均已保留",
		Items: []CatalogItem{{ID: "710000000000001", Title: "远端已读到"}},
	}
	tt := ui.NewTester(app.Render, 1200, 800)
	tt.Frame()
	if !tt.HasText("云端历史读取未完成，已读取和本机记录均已保留") || !tt.HasText("远端已读到") || !tt.HasText("立即重试") {
		t.Fatalf("partial sync render texts=%v", tt.Texts())
	}
}

func TestHistoryWithoutSyncErrorDoesNotShowRetryBanner(t *testing.T) {
	app := NewTestApp()
	app.view = "history"
	app.library.History = Library{
		Loaded: true,
		Items:  []CatalogItem{{ID: "710000000000001", Title: "保留的观看记录"}},
	}
	tt := ui.NewTester(app.Render, 1200, 800)
	if !tt.HasText("保留的观看记录") || tt.HasText("立即重试") || tt.HasText("账号归属") || tt.HasText("已下架") {
		t.Fatalf("background maintenance leaked into history: %v", tt.Texts())
	}
}

func TestCompletedHistoryRequiresRealFinalEpisodeProgress(t *testing.T) {
	rows, err := convertHistory([]ipc.HistoryItem{
		{SeriesID: "710000000000001", ContentType: 1, EpisodeNum: 2, TotalEpisodes: 2, PositionSec: 96, DurationSec: 100},
		{SeriesID: "710000000000002", EpisodeNum: 2, TotalEpisodes: 2, PositionSec: 96},
		{SeriesID: "710000000000003", EpisodeNum: 1, TotalEpisodes: 2, PositionSec: 100, DurationSec: 100},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	app := NewTestApp()
	app.library.History = Library{Items: rows}
	app.SetLibraryFilter("completed", "")
	completed := app.visibleLibrary(libraryHistory)
	if len(completed) != 1 || completed[0].ID != "710000000000001" {
		t.Fatalf("incorrect completed classification: %+v", completed)
	}
	app.SetLibraryFilter("unwatched", "")
	if len(app.visibleLibrary(libraryHistory)) != 2 {
		t.Fatal("unknown duration or intermediate episode classified as completed")
	}
}

// A slow favorites read must not prevent the shared header from learning likes.
type delayedCollections struct {
	fakeLibrary
	favoritesStarted chan struct{}
	likesStarted     chan struct{}
	releaseFavorites chan struct{}
}

func (f *delayedCollections) GetFavorites(context.Context) ([]ipc.FavoriteItem, error) {
	close(f.favoritesStarted)
	<-f.releaseFavorites
	return f.favorites, f.err
}
func (f *delayedCollections) GetLikes(context.Context) ([]ipc.LikeItem, error) {
	close(f.likesStarted)
	return f.likes, f.err
}

func TestEnteringEitherCollectionLoadsBothCounts(t *testing.T) {
	for _, tab := range []LibraryTab{libraryFavorites, libraryLikes} {
		app := NewTestApp()
		provider := &delayedCollections{
			fakeLibrary: fakeLibrary{
				favorites: []ipc.FavoriteItem{{SeriesID: "123456"}},
				likes:     []ipc.LikeItem{{SeriesID: "123456", EpisodeID: "654321"}},
			},
			favoritesStarted: make(chan struct{}), likesStarted: make(chan struct{}), releaseFavorites: make(chan struct{}),
		}
		app.libraryData = provider
		if libraryCountLabel(app.library.Likes) != "…" {
			t.Fatal("unloaded likes reported as zero")
		}
		done := make(chan struct{})
		go func() { app.loadLibrary(tab); close(done) }()
		for _, started := range []chan struct{}{provider.favoritesStarted, provider.likesStarted} {
			select {
			case <-started:
			case <-time.After(time.Second):
				close(provider.releaseFavorites)
				t.Fatal("both collections must start without waiting for the other")
			}
		}
		close(provider.releaseFavorites)
		<-done
		if libraryCountLabel(app.library.Favorites) != "1" || libraryCountLabel(app.library.Likes) != "1" {
			t.Fatalf("first entry counts: %+v", app.library)
		}
	}
}

func TestCollectionCountDistinguishesEmptyFailedAndCached(t *testing.T) {
	for _, tc := range []struct {
		library Library
		want    string
	}{
		{Library{Loaded: true, Empty: true}, "0"},
		{Library{Error: "read failed"}, "—"},
		{Library{Loaded: true, Error: "refresh failed", Items: []CatalogItem{{ID: "123456"}}}, "1"},
	} {
		if got := libraryCountLabel(tc.library); got != tc.want {
			t.Fatalf("count = %q, want %q", got, tc.want)
		}
	}
}

func TestVisibleLibraryIsTheSingleSourceForCountsAndGrid(t *testing.T) {
	items := []CatalogItem{
		{ID: "favorite-1", Title: "可见收藏一"},
		{ID: "favorite-2", Title: "可见收藏二"},
		{ID: "favorite-3", Title: "被过滤收藏"},
	}
	app := NewTestApp()
	app.SetInvalidator(func() {})
	app.library.Favorites = Library{Loaded: true, Items: items}
	app.library.History = Library{Loaded: true, Items: items}
	app.historyQuery = "可见"
	app.refreshVisibleLibraryLocked(libraryHistory)
	app.refreshVisibleLibraryLocked(libraryFavorites)
	if got := app.visibleLibrary(libraryHistory); len(got) != 2 || got[0].ID != "favorite-1" || got[1].ID != "favorite-2" {
		t.Fatalf("filtered favorites = %+v", got)
	}
	if len(app.library.Favorites.Items) != 3 {
		t.Fatalf("snapshot mutated: %+v", app.library.Favorites.Items)
	}
}

func TestHistoryHeaderMatchesFilteredGrid(t *testing.T) {
	items := []CatalogItem{
		{ID: "history-1", Title: "可见历史", Completed: false},
		{ID: "history-2", Title: "其他历史", Completed: true},
	}
	app := NewTestApp()
	app.SetInvalidator(func() {})
	app.library.History = Library{Loaded: true, Items: items}
	app.refreshVisibleLibraryLocked(libraryHistory)

	app.historyQuery = "可见"
	app.refreshVisibleLibraryLocked(libraryHistory)
	visible := app.visibleLibrary(libraryHistory)
	if len(visible) != 1 || visible[0].ID != "history-1" {
		t.Fatalf("filtered history = %#v", visible)
	}

	app.historyQuery = ""
	app.historyFilter = "completed"
	app.refreshVisibleLibraryLocked(libraryHistory)
	visible = app.visibleLibrary(libraryHistory)
	if len(visible) != 1 || visible[0].ID != "history-2" {
		t.Fatalf("completed history = %#v", visible)
	}
}

func TestReservationVisibleListAndCountsShareFilteredData(t *testing.T) {
	items := []CatalogItem{
		{ID: "online-1", Title: "已上线", Upcoming: false},
		{ID: "pending-1", Title: "待上线", Upcoming: true},
	}
	app := NewTestApp()
	app.SetInvalidator(func() {})
	app.library.Reserved = Library{Loaded: true, Items: items}
	app.reservationsPending = true
	app.refreshVisibleLibraryLocked(libraryReservations)
	visible := app.visibleLibrary(libraryReservations)
	if len(visible) != 1 || visible[0].ID != "pending-1" {
		t.Fatalf("pending reservations = %+v", visible)
	}
	if len(app.library.Reserved.Items) != 2 {
		t.Fatalf("reservation snapshot mutated: %+v", app.library.Reserved.Items)
	}
}

func TestReservationsOrderReleasedNewestAndPendingSoonest(t *testing.T) {
	app := NewTestApp()
	app.SetInvalidator(func() {})
	app.libraryData = &fakeLibrary{reservations: []ipc.ReservationItem{
		{SeriesID: "released-old", IsOnline: true, PublishAt: "20261118"},
		{SeriesID: "pending-later", PublishAt: "2026-12-02"},
		{SeriesID: "released-missing", IsOnline: true},
		{SeriesID: "released-new", IsOnline: true, PublishAt: "2026-11-20T09:00:00+08:00"},
		{SeriesID: "released-between", IsOnline: true, PublishAt: "2026-11-19T12:00:00"},
		{SeriesID: "pending-soon", PublishAt: "20261121"},
		{SeriesID: "released-same-time", IsOnline: true, PublishAt: "2026-11-20T01:00:00Z"},
		{SeriesID: "released-invalid", IsOnline: true, PublishAt: "待定"},
		{SeriesID: "pending-missing"},
	}}
	app.loadLibrary(libraryReservations)
	for _, step := range []struct {
		pending bool
		want    []string
	}{
		{false, []string{"released-new", "released-same-time", "released-between", "released-old", "released-missing", "released-invalid"}},
		{true, []string{"pending-soon", "pending-later", "pending-missing"}},
		{false, []string{"released-new", "released-same-time", "released-between", "released-old", "released-missing", "released-invalid"}},
	} {
		app.reservationsPending = step.pending
		app.refreshVisibleLibraryLocked(libraryReservations)
		items := app.visibleLibrary(libraryReservations)
		if len(items) != len(step.want) {
			t.Fatalf("pending=%t: got %d items, want %d", step.pending, len(items), len(step.want))
		}
		for i, id := range step.want {
			if items[i].ID != id {
				t.Fatalf("pending=%t: item %d = %s, want %s", step.pending, i, items[i].ID, id)
			}
		}
	}
}

func TestHistoryTabsRefreshCachedItems(t *testing.T) {
	app := NewTestApp()
	app.view = "history"
	app.library.History = Library{Loaded: true, Items: []CatalogItem{
		{ID: "unfinished", Title: "尚未看完的剧"},
		{ID: "finished", Title: "已经看完的剧", Completed: true},
	}}
	app.refreshVisibleLibraryLocked(libraryHistory)
	tt := ui.NewTester(app.Render, 1200, 800)
	tt.Frame()
	for _, step := range []struct {
		label string
		count int
		id    string
	}{
		{"已看完", 1, "finished"}, {"未看完", 1, "unfinished"}, {"全部", 2, "unfinished"},
	} {
		if err := tt.Click(step.label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Advance(500 * time.Millisecond)
		tt.Frame()
		rows := app.visibleLibrary(libraryHistory)
		if len(rows) != step.count || rows[0].ID != step.id {
			t.Fatalf("%s: got %+v", step.label, rows)
		}
	}
}
