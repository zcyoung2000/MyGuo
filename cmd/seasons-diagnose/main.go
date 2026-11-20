// seasons-diagnose compares the public official series row with SDK output.
// Device state stays in memory; no user account or library is loaded.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"myguo/internal/auth"
	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters/memstore"
	"net/url"
	"time"
)

type inspect struct{ inner hgapi.Transport }

func (t inspect) Do(ctx context.Context, req *hgapi.Request) (*hgapi.Response, error) {
	res, err := t.inner.Do(ctx, req)
	u, _ := url.Parse(req.URL)
	if u.Query().Get("from") == "single_col_same_ip_series" && res != nil {
		var env struct {
			Code any `json:"code"`
			Data []struct {
				ShowType  int `json:"show_type"`
				VideoData []struct {
					ID    string `json:"series_id"`
					Title string `json:"title"`
					Index int    `json:"season_index"`
				} `json:"video_data"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body, &env)
		b, _ := json.Marshal(env)
		fmt.Println("official-row", string(b))
	}
	return res, err
}
func main() {
	versions := flag.Bool("versions", false, "inspect source-verified adaptation candidates instead of seasons")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := hgapi.NewClient(hgapi.Options{Transport: inspect{auth.New()}, ProfileStore: memstore.New(), RetryAttempts: 1})
	if err != nil {
		panic(err)
	}
	for _, id := range flag.Args() {
		if *versions {
			inspectVersions(ctx, c, id)
			continue
		}
		album, err := c.Seasons(ctx, id)
		if err != nil {
			fmt.Println(id, err)
			continue
		}
		fmt.Printf("sdk series=%s count=%d\n", id, len(album.Seasons))
		for _, s := range album.Seasons {
			fmt.Printf("%d %s %s\n", s.Index, s.SeriesID, s.Title)
		}
	}
}

func inspectVersions(ctx context.Context, c *hgapi.Client, id string) {
	series, err := c.GetSeries(ctx, id)
	if err != nil {
		fmt.Println("detail", err)
		return
	}
	if series.Novel == nil {
		fmt.Println("source metadata absent", id)
		return
	}
	source := series.Novel.BookID
	fmt.Println("source", series.Novel.Title, source)
	cursor := ""
	seen := map[string]bool{}
	for pages := 0; pages < 10; pages++ {
		page, err := c.Search(ctx, series.Novel.Title, hgapi.SearchOptions{Tab: "剧集", PageSize: 30, Cursor: cursor})
		if err != nil {
			fmt.Println("search", err)
			return
		}
		fmt.Printf("page=%d candidates=%d more=%v\n", pages+1, len(page.Items), page.HasMore)
		ids := []string{}
		for _, item := range page.Items {
			if item.ID != "" && !seen[item.ID] {
				seen[item.ID] = true
				ids = append(ids, item.ID)
			}
		}
		if len(ids) > 0 {
			details, err := c.GetSeriesBatch(ctx, ids)
			if err != nil {
				fmt.Println("batch", err)
				return
			}
			for _, d := range details {
				if d.Series == nil {
					continue
				}
				v := d.Series
				if v.RelatedNovelBookID == source || (v.Novel != nil && v.Novel.BookID == source) {
					fmt.Printf("same-source id=%s same-album=%v current=%v title=%s\n", v.ID, series.RelatedAlbumID != "" && v.RelatedAlbumID == series.RelatedAlbumID, v.ID == id, v.Title)
				}
			}
		}
		if !page.HasMore || page.Cursor == "" || page.Cursor == cursor {
			return
		}
		cursor = page.Cursor
	}
}
