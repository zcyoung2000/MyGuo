package hgapi_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"myguo/internal/auth"
	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters/memstore"
)

// Exercise production filter encoding, signing and account transport together.
func TestArtStyleIsSignedAndRetainedAcrossPagination(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		payload, _ := io.ReadAll(r.Body)
		var body struct {
			Scene      string              `json:"req_scene"`
			Offset     int                 `json:"offset"`
			Selections map[string][]string `json:"select_items"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Error(err)
		}
		if body.Scene != "comic_series" || strings.Join(body.Selections["category_dim_art_style"], ",") != "cate_5415" || strings.Join(body.Selections["genre"], ",") != "comic_series" || body.Offset != (calls-1)*18 {
			t.Errorf("wrong signed query: %s", payload)
		}
		sum := md5.Sum(payload)
		if !strings.EqualFold(r.Header.Get("x-ss-stub"), hex.EncodeToString(sum[:])) || r.Header.Get("x-medusa") == "" || r.Header.Get("x-gorgon") == "" {
			t.Error("modified body was not signed")
		}
		if r.Header.Get("Cookie") != "sessionid=fixture" {
			t.Error("lost account header")
		}
		fmt.Fprintf(w, `{"code":0,"data":{"has_more":true,"next_offset":%d,"session_id":"fixture","video_data":[{"series_id":"710000000000010%d","title":"漫画","episode_cnt":48}]}}`, calls*18, calls)
	}))
	defer server.Close()
	profiles := memstore.New()
	_ = profiles.Save(context.Background(), &hgapi.DeviceProfile{DeviceID: "7100000000000001", InstallID: "7100000000000002", TTReq: "fixture"})
	transport := auth.New()
	transport.SetCredentials("sessionid=fixture", "", "cn")
	client, err := hgapi.NewClient(hgapi.Options{Transport: transport, ProfileStore: profiles, DistributionOrigin: server.URL, RetryAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string][]string{"genre": {"comic_series"}, "category_dim_art_style": {"cate_5415"}}
	first, err := client.ShortFilter(context.Background(), hgapi.ShortFilterQuery{Selections: values})
	if err != nil || first == nil || first.Cursor == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := client.ShortFilter(context.Background(), hgapi.ShortFilterQuery{Selections: values, Cursor: first.Cursor})
	if err != nil || second == nil || calls != 2 {
		t.Fatalf("second=%+v calls=%d err=%v", second, calls, err)
	}
	changed := map[string][]string{"genre": {"comic_series"}, "category_dim_art_style": {"cate_5416"}}
	if _, err := client.ShortFilter(context.Background(), hgapi.ShortFilterQuery{Selections: changed, Cursor: first.Cursor}); err == nil {
		t.Fatal("changed style reused the prior cursor")
	}
	if _, err := client.ShortFilter(context.Background(), hgapi.ShortFilterQuery{Selections: map[string][]string{"genre": {"comic_series"}}, Cursor: first.Cursor}); err == nil {
		t.Fatal("cleared style reused the prior cursor")
	}
	if calls != 2 {
		t.Fatal("invalid cursor reached upstream")
	}
}
