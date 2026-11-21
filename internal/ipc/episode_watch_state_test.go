package ipc

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"myguo/internal/resourcecache"
	userstore "myguo/internal/store"
)

func TestCachedSeriesPageRefreshesCrossDevicePartialWatchState(t *testing.T) {
	service, store := accountTestService(t, t.TempDir(), t.TempDir())
	const seriesID = "710000000000001"
	calls := 0
	service.httpClient = &http.Client{Transport: detailAPITestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != "POST" || request.URL.Path != "/reading/bookapi/read_progress/get/v" {
			t.Errorf("unexpected request while reusing cached metadata: %s %s", request.Method, request.URL.Path)
		}
		calls++
		return detailResponse([]byte(fmt.Sprintf(`{"code":0,"data":[],"video_data":[{"series_id":%q,"vid_index":%d,"episode_count":112,"current_play_time":500,"last_watch_time":%d}]}`, seriesID, calls, calls*1000))), nil
	})}
	if err := store.SetAuthenticatedAccount(userstore.AccountAuth{Cookies: "sessionid=test"}, UserProfile{UserID: "test", LoggedIn: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := service.restoreSavedAccount(); err != nil {
		t.Fatal(err)
	}
	cached := &SeriesDetailPageDTO{SeriesDetailDTO: SeriesDetailDTO{
		ID: seriesID, Title: "Cached catalog", EpisodeCount: 112,
		Episodes: []EpisodeDTO{{Number: 1}, {Number: 2}, {Number: 112}},
	}}
	service.cache.Set("series_page:"+seriesID, resourcecache.Item{Value: cached, ExpiresAt: time.Now().Add(5 * time.Minute)})
	for count := 1; count <= 2; count++ {
		page, err := service.GetSeriesPage(context.Background(), seriesID)
		if err != nil || page == nil {
			t.Fatalf("cached detail failed: %+v, %v", page, err)
		}
		if calls != count || page.ResumeEpisode != count || page.ResumePosition != .5 {
			t.Fatalf("cached metadata skipped cloud progress: calls=%d, page=%+v", calls, page)
		}
		for index, ep := range page.Episodes {
			if ep.Watched != (index < count) {
				t.Fatalf("remote partial watch state = %+v; want exactly %d visited episodes", page.Episodes, count)
			}
		}
	}
	for _, ep := range cached.Episodes {
		if ep.Watched {
			t.Fatal("cloud progress mutated shared catalog metadata")
		}
	}
}

func TestSeriesWatchStateDoesNotNeedKnownDuration(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const seriesID = "710000000000001"
	if _, err := store.SavePlaybackProgress(userstore.HistoryItem{SeriesID: seriesID, EpisodeNum: 2, PositionSec: .1}, "episode-2", ""); err != nil {
		t.Fatal(err)
	}
	service := &HongguoService{store: store}
	page := service.withSeriesUserState(&SeriesDetailPageDTO{SeriesDetailDTO: SeriesDetailDTO{ID: seriesID, Episodes: []EpisodeDTO{{Number: 1}, {Number: 2}, {Number: 3}}}})
	if page.Episodes[0].Watched || !page.Episodes[1].Watched || page.Episodes[2].Watched {
		t.Fatalf("partial playback without duration = %+v", page.Episodes)
	}
}
