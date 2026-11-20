package ipc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters"
	"myguo/internal/hgapi/adapters/memstore"
)

func TestDeniedPlaybackModelDoesNotReturnUnusableMediaURLs(t *testing.T) {
	t.Setenv("MYGUO_DATA_ROOT", t.TempDir())
	t.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	var postCalls, tabCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "device_register"):
			_, _ = w.Write([]byte(`{"device_id_str":"710000000000001","install_id_str":"710000000000002","new_user":1}`))
		case strings.Contains(r.URL.Path, "video_model"):
			if r.Method == http.MethodGet && r.URL.Path == hgapi.VideoTabPath {
				tabCalls.Add(1)
			} else {
				postCalls.Add(1)
			}
			w.WriteHeader(http.StatusOK) // The observed upstream failure: zero bytes.
		default:
			_, _ = w.Write([]byte(`{"code":0,"data":{"video_data":{"series_id_str":"710000000000010","series_title":"测试剧","episode_cnt":1,"video_list":[{"vid":"710000000000101","vid_index":0,"series_id":"710000000000010","disable_play":false}]}}}`))
		}
	}))
	defer server.Close()
	zero := 0
	client, err := adapters.NewClient("", hgapi.Options{ProfileStore: memstore.New(), APIOrigin: server.URL, RegisterOrigin: server.URL, RetryAttempts: 1, DeviceResets: &zero})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	media, err := NewMediaServer(client)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	service := &HongguoService{store: store, media: media, client: client}
	dto, err := service.PlayEpisode(context.Background(), "710000000000010", "", 1, "1080p", true)
	if dto != nil || !errors.Is(err, ErrPlaybackAccessLimited) || !errors.Is(err, hgapi.ErrAnonymousLimited) {
		t.Fatalf("denied model returned a playable selection: dto=%v error=%v", dto != nil, err)
	}
	if postCalls.Load() != 1 || tabCalls.Load() != 1 {
		t.Fatalf("denied playback repeated or omitted a route: POST=%d GET=%d", postCalls.Load(), tabCalls.Load())
	}
}
