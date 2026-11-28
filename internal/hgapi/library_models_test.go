package hgapi

import (
	"strings"
	"testing"
)

func TestParseFavoritesResponseKeepsPartialPage(t *testing.T) {
	for _, body := range []string{
		`{"code":0,"has_more":true,"data":{"video_shelf_info":[{"series_id":"710000000000001"}]}}`,
		`{"code":0,"data":{"has_more":true,"video_shelf_info":[{"series_id":"710000000000001"}]}}`,
	} {
		response, err := parseFavoritesResponse([]byte(body))
		if err != nil {
			t.Fatalf("parseFavoritesResponse(%s) error = %v", body, err)
		}
		if !response.HasMore || response.Data == nil || response.Data.VideoShelfInfo == nil || len(*response.Data.VideoShelfInfo) != 1 {
			t.Fatalf("partial favorites response = %#v", response)
		}
	}
}

func TestParseFavoritesResponseRejectsMissingShelf(t *testing.T) {
	_, err := parseFavoritesResponse([]byte(`{"code":0,"data":{"has_more":false}}`))
	if err == nil || !strings.Contains(err.Error(), "不完整") {
		t.Fatalf("parseFavoritesResponse() error = %v, want incomplete shelf error", err)
	}
}
