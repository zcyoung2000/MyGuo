// catalog-diagnose reads public filter taxonomy and optional result pages using
// the production SDK/account transport, with anonymous device state in memory.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"myguo/internal/auth"
	"myguo/internal/catalog"
	"myguo/internal/hgapi"
	"myguo/internal/hgapi/adapters/memstore"
)

type inspect struct {
	inner      hgapi.Transport
	genre, dir string
}

func (t inspect) Do(ctx context.Context, req *hgapi.Request) (*hgapi.Response, error) {
	u, _ := url.Parse(req.URL)
	panel := strings.Contains(u.Path, "/category/landpage/")
	var kind string
	if panel {
		var body struct {
			Scene string `json:"req_scene"`
			Type  string `json:"req_type"`
		}
		_ = json.Unmarshal(req.Body, &body)
		kind = body.Type
		fmt.Fprintf(os.Stderr, "genre=%s scene=%s request=%s\n", t.genre, body.Scene, kind)
	}
	res, err := t.inner.Do(ctx, req)
	if panel && kind == "only_panel" && res != nil && t.dir != "" {
		if err := os.WriteFile(filepath.Join(t.dir, t.genre+".json"), res.Body, 0600); err != nil {
			return nil, err
		}
	}
	return res, err
}
func run() error {
	dir := flag.String("out", "", "optional directory for public panel responses")
	verify := flag.Bool("verify-content", false, "also read default, themed and art-style results and a second page")
	flag.Parse()
	if *dir != "" {
		if err := os.MkdirAll(*dir, 0700); err != nil {
			return err
		}
	}
	for _, genre := range []string{"short_play", "comic_series", "ai_series"} {
		client, err := hgapi.NewClient(hgapi.Options{Transport: inspect{auth.New(), genre, *dir}, ProfileStore: memstore.New(), RetryAttempts: 3})
		if err != nil {
			return err
		}
		service := catalog.NewService(client, func(s string) string { return s }, nil, catalog.Dependencies{})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		// Initial device registration has its own SDK timeout, before the
		// production service's shorter per-page timeout starts.
		if _, err := client.ShortFilterPanel(ctx, genre); err != nil {
			cancel()
			return err
		}
		rows, err := service.GetFilterPanel(ctx, genre)
		if err != nil {
			cancel()
			return fmt.Errorf("%s: %w", genre, err)
		}
		encoded, _ := json.Marshal(struct {
			Genre string `json:"genre"`
			Rows  any    `json:"rows"`
		}{genre, rows})
		fmt.Println(string(encoded))
		if *verify {
			queries := []map[string]string{{}}
			foundStyle := false
			for _, row := range rows {
				if (row.Key == "category_dim_theme" || row.Key == "category_dim_art_style") && len(row.Options) > 0 {
					queries = append(queries, map[string]string{row.Key: row.Options[0].Value})
					foundStyle = foundStyle || row.Key == "category_dim_art_style"
				}
			}
			if genre == "comic_series" && !foundStyle {
				// Also exercise the art-style field observed in the November 14
				// official panel snapshot. Device variants can omit this row.
				queries = append(queries, map[string]string{"category_dim_art_style": "cate_5415"})
			}
			for _, query := range queries {
				page, err := service.GetDiscover(ctx, genre, query, "")
				if err != nil {
					cancel()
					return fmt.Errorf("%s %v: %w", genre, query, err)
				}
				fmt.Fprintf(os.Stderr, "verified genre=%s selections=%v items=%d more=%v\n", genre, query, len(page.Items), page.HasMore)
				if len(page.Items) == 0 {
					cancel()
					return fmt.Errorf("empty first page for %s %v", genre, query)
				}
				if page.HasMore && page.Cursor != "" {
					next, err := service.GetDiscover(ctx, genre, query, page.Cursor)
					if err != nil {
						cancel()
						return err
					}
					fmt.Fprintf(os.Stderr, "verified genre=%s page=2 items=%d\n", genre, len(next.Items))
				}
			}
		}
		cancel()
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
