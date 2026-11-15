package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beilunyang/openhongguo/adapters/memstore"
	"github.com/beilunyang/openhongguo/sdk"
	"myguo/internal/auth"
	"myguo/internal/resourcecache"
)

type detailAPITestTransport func(*http.Request) (*http.Response, error)

func (t detailAPITestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }

const detailTestSeries = "7695330621513534489"

func detailFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func detailTestService(t *testing.T, handler func(*http.Request) (*http.Response, error)) *HongguoService {
	t.Helper()
	store, e := NewStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	return &HongguoService{store: store, media: &MediaServer{}, cache: resourcecache.New(16), httpClient: &http.Client{Transport: detailAPITestTransport(handler)}}
}
func detailResponse(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestOfficialAdaptationsUseSceneWithoutSearchOrSourceMetadata(t *testing.T) {
	calls := 0
	s := detailTestService(t, func(r *http.Request) (*http.Response, error) {
		calls++
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/reading/bookapi/plan/v1/" || q.Get("from") != "detail_page_more_related" || q.Get("book_id") != detailTestSeries || q.Get("filter_ids") != detailTestSeries {
			t.Errorf("wrong official route: %s %s", r.Method, r.URL.Path)
		}
		return detailResponse(detailFixture(t, "official_related.json")), nil
	})
	for i := 0; i < 2; i++ {
		page, e := s.GetSeriesAdaptations(context.Background(), detailTestSeries, "", "", "", "")
		if e != nil || len(page.Items) != 5 || len(page.Seasons) != 2 || page.HasMore {
			t.Fatalf("official works: %+v %v", page, e)
		}
		if page.Seasons[0].ID != "7700569108542147608" || page.Seasons[0].BadgeText != "第2季" || !page.Seasons[1].Upcoming {
			t.Fatalf("season metadata lost: %+v", page.Seasons)
		}
		for _, v := range page.Items {
			if strings.HasPrefix(v.Title, "十八岁的二次回响") || v.ID == "99999999" {
				t.Fatalf("season or guess admitted: %+v", v)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("requests=%d, expected one cached official request and no SDK/search/detail requests", calls)
	}
}
func TestOfficialAdaptationPaginationIsScopedToSeries(t *testing.T) {
	raw := detailFixture(t, "official_related.json")
	var env map[string]any
	json.Unmarshal(raw, &env)
	cell := env["data"].([]any)[0].(map[string]any)
	cell["has_more"] = true
	cell["next_offset"] = 7
	raw, _ = json.Marshal(env)
	calls := 0
	s := detailTestService(t, func(r *http.Request) (*http.Response, error) { calls++; return detailResponse(raw), nil })
	p, e := s.GetSeriesAdaptationsPage(context.Background(), detailTestSeries, "", "", "", "", "")
	if e != nil || !p.HasMore || p.Cursor != "related1:"+detailTestSeries+":7" {
		t.Fatalf("cursor lost: %+v %v", p, e)
	}
	if _, e = s.GetSeriesAdaptationsPage(context.Background(), "12345678", "", "", "", "", p.Cursor); e == nil {
		t.Fatal("cross-series cursor accepted")
	}
	if calls != 1 {
		t.Fatal("invalid cursor made a request")
	}
	cell["next_offset"] = 0
	raw, _ = json.Marshal(env)
	if _, e = s.officialAdaptations(context.Background(), detailTestSeries, "", nil); e == nil {
		t.Fatal("nonadvancing cursor accepted")
	}
}
func TestOfficialAdaptationFailuresAreNotCachedAsEmpty(t *testing.T) {
	calls := 0
	s := detailTestService(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return detailResponse([]byte(`{"code":101003,"message":"failed"}`)), nil
	})
	for i := 0; i < 2; i++ {
		if _, e := s.GetSeriesAdaptations(context.Background(), detailTestSeries, "", "", "", ""); e == nil {
			t.Fatal("failure became empty success")
		}
	}
	if calls != 2 {
		t.Fatal("failure was cached")
	}
}
func TestReviewParsingPreservesScoresAndRejectsWrongOwnership(t *testing.T) {
	raw := detailFixture(t, "series_reviews.json")
	page, e := parseReviewPage(raw, detailTestSeries, "")
	if e != nil || len(page.Items) != 2 || page.Total != 911 || !page.HasMore || page.Items[0].Score != 10 || page.Items[0].AvatarURL != "https://images.example/avatar.jpg" || page.ScoreCount != 0 {
		t.Fatalf("review metadata lost: %+v %v", page, e)
	}
	if wrong, e := parseReviewPage(raw, "12345678", ""); e != nil || len(wrong.Items) != 0 {
		t.Fatalf("foreign reviews admitted: %+v %v", wrong, e)
	}
	for _, value := range []string{`"NaN"`, `"Inf"`, `-1`, `null`} {
		if apiNumber(json.RawMessage(value)) != 0 {
			t.Fatalf("invalid score %s accepted", value)
		}
	}
}

func TestReviewDuplicateCannotReplaceValidatedMetadata(t *testing.T) {
	row := func(group, score string) string {
		return fmt.Sprintf(`{"comment":{"comment_id":"review-one","common":{"group_id":%q,"status":1,"content":{"text":"valid review"}},"expand":{"book_id":%q,"score":%q}}}`, group, group, score)
	}
	raw := []byte(`{"code":0,"data":{"data_list":[` + row("12345678", "1") + "," + row(detailTestSeries, "10") + "," + row("12345678", "9") + `]}}`)
	page, err := parseReviewPage(raw, detailTestSeries, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Score != 10 {
		t.Fatalf("duplicate overrode validated review: %+v %v", page, err)
	}
}
func TestReviewRequestUsesSeriesGroupAndPreservesContinuation(t *testing.T) {
	raw := detailFixture(t, "series_reviews.json")
	cursor := `{"session_id":"fixture","offset":5}`
	s := detailTestService(t, func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if r.Method != "POST" || r.URL.Path != "/novel/commentapi/comment/list/"+detailTestSeries+"/v1/" || body["group_id"] != detailTestSeries || body["group_type"] != float64(1) || body["comment_type"] != float64(2) || body["comment_source"] != float64(1) || body["server_channel"] != float64(34) || body["cursor"] != cursor {
			t.Errorf("incorrect review scope: %s %+v", r.URL.Path, body)
		}
		return detailResponse(raw), nil
	})
	page, e := s.GetSeriesReviews(context.Background(), detailTestSeries, cursor)
	if e != nil || len(page.Items) != 2 {
		t.Fatalf("reviews: %+v %v", page, e)
	}
}

func TestLiveOfficialDetailAPIs(t *testing.T) {
	if os.Getenv("MYGUO_LIVE_DETAIL") != "1" {
		t.Skip("explicit live read-only verification")
	}
	root := t.TempDir()
	t.Setenv("MYGUO_DATA_ROOT", root)
	profiles := memstore.New()
	client, e := sdk.NewClient(sdk.Options{Transport: auth.New(), ProfileStore: profiles, RetryAttempts: 2})
	if e != nil {
		t.Fatal(e)
	}
	store, e := NewStore(root)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	s := NewHongguoService(client, store, &MediaServer{}, root, WithDeviceProfileStore(profiles))
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	if _, e = s.GetSeriesPage(ctx, detailTestSeries); e != nil {
		t.Fatal(e)
	}
	works, e := s.GetSeriesAdaptations(ctx, detailTestSeries, "", "", "", "")
	if e != nil || len(works.Items) == 0 {
		t.Fatalf("official works: %+v %v", works, e)
	}
	page, e := s.GetSeriesReviews(ctx, detailTestSeries, "")
	if e != nil || len(page.Items) == 0 {
		t.Fatalf("reviews: %+v %v", page, e)
	}
	t.Logf("official adaptations=%d reviews=%d total=%d more=%v", len(works.Items), len(page.Items), page.Total, page.HasMore)
	if page.HasMore {
		next, e := s.GetSeriesReviews(ctx, detailTestSeries, page.NextCursor)
		if e != nil || len(next.Items) == 0 {
			t.Fatalf("next review page: %+v %v", next, e)
		}
		t.Logf("next review page=%d", len(next.Items))
	}
	for _, review := range page.Items {
		if review.ReplyCount > 0 {
			replies, e := s.GetSeriesReviewReplies(ctx, detailTestSeries, review.ID, "")
			if e != nil {
				t.Fatalf("replies: %v", e)
			}
			t.Logf("replies=%d more=%v", len(replies.Items), replies.HasMore)
			break
		}
	}
	if works.HasMore {
		t.Log(fmt.Sprintf("official works continuation=%s", works.Cursor))
	}
}

func TestLiveOfficialRelatedGroups(t *testing.T) {
	if os.Getenv("MYGUO_LIVE_DETAIL") != "1" {
		t.Skip("explicit live read-only verification")
	}
	root := t.TempDir()
	t.Setenv("MYGUO_DATA_ROOT", root)
	profiles := memstore.New()
	client, err := sdk.NewClient(sdk.Options{Transport: auth.New(), ProfileStore: profiles, RetryAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := NewHongguoService(client, store, &MediaServer{}, root, WithDeviceProfileStore(profiles))
	defer CloseService(s)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if _, err = s.GetSeriesPage(ctx, detailTestSeries); err != nil {
		t.Fatal(err)
	}
	// The memory profile store lacks the production SQLite saved hook.
	if err = s.reloadDeviceIdentity(); err != nil {
		t.Fatal(err)
	}
	page, err := s.GetSeriesAdaptations(ctx, detailTestSeries, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Seasons) == 0 || len(page.Items) == 0 {
		t.Fatalf("missing related group: seasons=%d adaptations=%d", len(page.Seasons), len(page.Items))
	}
	seen := map[string]bool{detailTestSeries: true}
	for _, item := range page.Seasons {
		if seen[item.ID] {
			t.Fatal("current or repeated season")
		}
		seen[item.ID] = true
		t.Logf("other season: %s (%s)", item.Title, item.BadgeText)
	}
	for _, item := range page.Items {
		if seen[item.ID] {
			t.Fatal("adaptations overlap with seasons")
		}
	}
	t.Logf("single related page: other seasons=%d adaptations=%d more=%v", len(page.Seasons), len(page.Items), page.HasMore)
}

func TestRelatedSeasonsIgnoreAdaptationExclusionsAndRejectCurrentAndDuplicates(t *testing.T) {
	var env map[string]any
	if err := json.Unmarshal(detailFixture(t, "official_related.json"), &env); err != nil {
		t.Fatal(err)
	}
	cell := env["data"].([]any)[0].(map[string]any)
	rows := cell["video_data"].([]any)
	cell["video_data"] = append(rows, rows[0], map[string]any{"series_id": detailTestSeries, "title": "当前季", "tag_info": map[string]any{"text": "第1季"}}, map[string]any{"series_id": "12345678", "title": "未知关系", "season_index": 2})
	raw, _ := json.Marshal(env)
	s := detailTestService(t, func(*http.Request) (*http.Response, error) { return detailResponse(raw), nil })
	page, err := s.GetSeriesAdaptations(context.Background(), detailTestSeries, "", "", "", "7700569108542147608,7696515003951631385")
	if err != nil || len(page.Seasons) != 2 || len(page.Items) != 4 {
		t.Fatalf("wrong relation filtering: %+v %v", page, err)
	}
}
