package hgapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func videoProgressResponse(count int, clock int64) []byte {
	rows := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, map[string]any{
			"series_id": fmt.Sprintf("71000000000%04d", i), "vid_index": 55,
			"episode_count": 90, "current_play_time": 71738, "last_watch_time": clock - int64(i),
		})
	}
	raw, _ := json.Marshal(map[string]any{"code": 0, "data": []any{}, "video_data": rows})
	return raw
}

func TestNativeHistoryUsesVideoDataAndNormalizesEpisodeIndex(t *testing.T) {
	page, err := ParseHistoryPage(videoProgressResponse(1, 1794914202742))
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("native progress lost behind empty data: %+v, %v", page, err)
	}
	row := page.Items[0]
	if row.VidIndex != 54 || row.CurrentPlayTime != 71738 || row.EpisodeCount != 90 || page.HasMore {
		t.Fatalf("native progress units changed: %+v", page)
	}
	if _, err := ParseHistoryPage([]byte(`{"code":0,"data":[],"video_data":[{"series_id":"710000000000001","vid_index":1}]}`)); err == nil {
		t.Fatal("incomplete native record accepted")
	}
}

func TestNativeHistoryUsesDescendingWatchTimestampPagination(t *testing.T) {
	calls := 0
	const firstClock int64 = 1794914202742
	api := NewSessionClient(&http.Client{Transport: sessionTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/reading/bookapi/read_progress/list/v" || q.Get("book_types") != "2" || q.Get("limit") != "50" || q.Get("offset") != "" {
			t.Fatal("history uses an unrelated endpoint or offset protocol")
		}
		if calls == 2 && q.Get("last_min_read_timestamp_ms_list") != strconv.FormatInt(firstClock-49, 10) {
			t.Fatal("history timestamp cursor was replaced by a row offset")
		}
		count, clock := 50, firstClock
		if calls == 2 {
			count, clock = 1, firstClock-100
		}
		return apiTestResponse(string(videoProgressResponse(count, clock))), nil
	})}, testSession())
	first, err := api.History(context.Background(), 0, "")
	if err != nil || !first.HasMore || first.NextOffset != firstClock-49 {
		t.Fatalf("first history page: %+v, %v", first, err)
	}
	second, err := api.History(context.Background(), first.NextOffset, "")
	if err != nil || second.HasMore || second.NextOffset >= first.NextOffset || len(second.Items) != 1 {
		t.Fatalf("older history page: %+v, %v", second, err)
	}
}
