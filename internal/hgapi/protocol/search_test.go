package protocol

import (
	"fmt"
	"testing"
)

func TestVideoSearchFiltersNonSeriesAndMergesRepeatedSeries(t *testing.T) {
	const rows = `[
		{"video_data":[{"series_id":"A","video_detail":{"series_title":"同名短剧","episode_cnt":80,"video_detail_list":[{"series_id_str":"A","series_title":"同名短剧","series_cover":"cover-A","episode_cnt":80}]}}]},
		{"book_data":[{"book_id":"novel","book_name":"原著小说","serial_count":999}],"celebrity_user_list":[{"user_id":"user","base_info":{"user_name":"演员"}}],"post_data":[{"post_id":"post","title":"脑洞漫画推荐"}]},
		{"video_data":[{"series_id":"A","video_detail":{"series_title":"同名短剧","series_cover":"other-cover"}},{"series_id":"B","video_detail":{"series_title":"同名短剧","series_cover":"cover-B","episode_cnt":40}},{"title":"讨论推广","series_id":"promotion","video_detail":{}}]},
		{"video_data":[{"video_detail":{"video_detail_list":[{"series_id":123,"series_title":"漫剧第三季","series_cover":"cover-C","episode_cnt":152}]}}]}
	]`
	for _, tab := range []int{11, 29, 30} {
		page, err := ParseSearchTab([]byte(fmt.Sprintf(`{"code":0,"search_tabs":[{"tab_type":%d,"has_more":true,"next_offset":20,"data":%s}]}`, tab, rows)), tab)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 3 || page.Items[0].ID != "A" || page.Items[1].ID != "B" || page.Items[2].ID != "123" {
			t.Fatalf("video tab %d retained mixed or duplicate cells: %+v", tab, page.Items)
		}
		if page.Items[0].CoverURL != "cover-A" || page.Items[0].EpisodeCount != 80 || page.Items[2].CoverURL != "cover-C" {
			t.Fatalf("merged series lost video metadata: %+v", page.Items)
		}
		if !page.HasMore || page.NextOffset != 20 {
			t.Fatal("filtering changed upstream pagination")
		}
	}
}

func TestExplicitNonVideoSearchTabsKeepTheirResults(t *testing.T) {
	for _, fixture := range []struct {
		tab     int
		row, id string
	}{
		{1, `"book_data":[{"book_id":"book","book_name":"小说"}]`, "book"},
		{27, `"celebrity_user_list":[{"user_id":"user","base_info":{"user_name":"演员"}}]`, "user"},
		{31, `"post_data":[{"post_id":"post","title":"讨论"}]`, "post"},
	} {
		page, err := ParseSearchTab([]byte(fmt.Sprintf(`{"code":0,"search_tabs":[{"tab_type":%d,"data":[{%s}]}]}`, fixture.tab, fixture.row)), fixture.tab)
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != fixture.id {
			t.Fatalf("explicit tab %d lost its content: %+v / %v", fixture.tab, page, err)
		}
	}
}
