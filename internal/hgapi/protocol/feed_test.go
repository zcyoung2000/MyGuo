package protocol

import "testing"

func TestVideoFeedDoesNotTreatSourceNovelsAsPlayableSeries(t *testing.T) {
	const cell = `{"video_data":[{"series_id":"710000000000001","video_detail":{"series_title":"短剧","episode_cnt":3}}],"book_data":[{"book_id":"710000000000002","book_name":"原著小说","serial_count":999}]}`
	for _, tab := range []int{16, 36, 39} {
		page, err := ParseCellChangeForTab([]byte(`{"code":0,"data":{"has_more":true,"next_offset":10,"cell_view":{"cell_data":[`+cell+`]}}}`), tab)
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != "710000000000001" {
			t.Fatalf("video tab %d admitted a novel as a series: %+v / %v", tab, page, err)
		}
	}
	page, err := ParseDiscoverTab([]byte(`{"code":0,"data":{"tab_item":[{"tab_type":2,"cell_data":[`+cell+`]}]}}`), 2)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("novel catalog lost books: %+v / %v", page, err)
	}
}
