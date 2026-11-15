package uiapp

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

type scrollPageProvider struct {
	fakeDetail
	calls chan string
	pages chan ipc.DiscoverResponse
}

func (p *scrollPageProvider) GetDiscover(ctx context.Context, _ string, _ map[string]string, cursor string) (ipc.DiscoverResponse, error) {
	p.calls <- cursor
	select {
	case page := <-p.pages:
		return page, nil
	case <-ctx.Done():
		return ipc.DiscoverResponse{}, ctx.Err()
	}
}
func scrollCards(first, count int) []CatalogItem {
	items := make([]CatalogItem, count)
	for i := range items {
		items[i] = CatalogItem{ID: fmt.Sprint(first + i), Title: fmt.Sprintf("稳定卡片%d", first+i), EpisodeCount: 80}
	}
	return items
}
func TestCatalogPaginationKeepsVisibleCardAndOnlyLoadsOncePerScroll(t *testing.T) {
	p := &scrollPageProvider{calls: make(chan string, 10), pages: make(chan ipc.DiscoverResponse, 10)}
	a := NewTestApp()
	a.detailSource = p
	a.catalog.Items = scrollCards(0, 80)
	a.catalog.HasMore = true
	a.catalog.Cursor = "next"
	a.catalog.resultsCurrent = true
	a.discoverFilterManual = true
	a.discoverFilterState = discoverCollapsed
	tt := scrollInputTester(a, 1600, 1000)
	tt.Frame()
	viewport, _ := tt.AccessByRole(ui.RoleList, "discover-page-scroll")
	columns := dramaColumns(viewport.Bounds.W)
	a.catalogList.ScrollTo(1+len(a.catalog.Items)/columns-3, ui.Start)
	tt.Frame()
	tt.Frame()
	list, ok := tt.AccessByRole(ui.RoleList, "discover-page-scroll")
	if !ok {
		t.Fatal("missing native list")
	}
	tt.Wheel(list.Bounds.X+list.Bounds.W/2, list.Bounds.Y+list.Bounds.H/2, 0, 80)
	select {
	case cursor := <-p.calls:
		if cursor != "next" {
			t.Fatal(cursor)
		}
	case <-time.After(time.Second):
		t.Fatal("wheel did not load next page")
	}
	tt.Frame()
	var anchor ui.AccessInfo
	for _, node := range tt.AccessAll() {
		if node.Role == ui.RoleButton && strings.HasPrefix(node.Name, "稳定卡片") && node.Bounds.Y > list.Bounds.Y+10 && node.Bounds.Y+node.Bounds.H < list.Bounds.Y+list.Bounds.H-10 {
			anchor = node
			break
		}
	}
	if anchor.Name == "" {
		t.Fatal("no fully visible card")
	}
	check := func() {
		t.Helper()
		tt.Frame()
		node, ok := tt.Access(anchor.Name)
		if !ok || math.Abs(float64(node.Bounds.Y-anchor.Bounds.Y)) > .5 {
			t.Fatalf("visible card moved during pagination: %s before=%v after=%v present=%v", anchor.Name, anchor.Bounds, node.Bounds, ok)
		}
	}
	for range 8 {
		check()
	}
	assertQuietLoading(t, tt)
	items := scrollCards(80, 30)
	cards := make([]ipc.SeriesCard, len(items))
	for i, item := range items {
		cards[i] = ipc.SeriesCard{ID: item.ID, Title: item.Title, EpisodeCount: item.EpisodeCount}
	}
	p.pages <- ipc.DiscoverResponse{Items: cards, Cursor: "after", HasMore: true}
	waitFor(time.Second, func() bool { return len(a.CatalogState().Items) == 110 })
	if len(a.CatalogState().Items) != 110 {
		t.Fatal("page did not append")
	}
	for range 15 {
		check()
		runtime.Gosched()
	}
	tt.Advance(time.Second)
	check()
	assertQuietLoading(t, tt)
	if len(p.calls) != 0 {
		t.Fatal("repaint or append loaded another page without user scrolling")
	}
}

func TestAutomaticFilterCollapseSettlesWithoutScrollOscillation(t *testing.T) {
	a := NewTestApp()
	a.catalog.Items = scrollCards(0, 80)
	for i := range 6 {
		a.catalog.Filters = append(a.catalog.Filters, FilterRow{Key: fmt.Sprint(i), Title: fmt.Sprintf("条件%d", i), Options: []FilterOption{{Label: "全部", Value: ""}, {Label: "测试", Value: "one"}}})
	}
	tt := scrollInputTester(a, 1280, 820)
	tt.Frame()
	node, _ := tt.AccessByRole(ui.RoleList, "discover-page-scroll")
	tt.Wheel(node.Bounds.X+100, node.Bounds.Y+40, 0, 120)
	for range 30 {
		tt.Advance(20 * time.Millisecond)
		tt.Frame()
		if a.discoverFilterState != discoverCollapsed {
			t.Fatal("filter re-expanded during downward scroll")
		}
	}
	y := a.discoverPageScroll.Y
	for range 10 {
		tt.Frame()
		if math.Abs(float64(a.discoverPageScroll.Y-y)) > .5 {
			t.Fatalf("scroll oscillated after settling: %v -> %v", y, a.discoverPageScroll.Y)
		}
	}
}

func TestShortCatalogPageFillsViewportWithoutLoadingControls(t *testing.T) {
	p := &scrollPageProvider{calls: make(chan string, 10), pages: make(chan ipc.DiscoverResponse, 10)}
	a := NewTestApp()
	a.detailSource = p
	a.catalog.Items = scrollCards(0, 1)
	a.catalog.Cursor = "next"
	a.catalog.HasMore = true
	a.catalog.resultsCurrent = true
	a.discoverFilterManual = true
	a.discoverFilterState = discoverCollapsed
	tt := scrollInputTester(a, 1600, 1000)
	tt.Frame()
	select {
	case <-p.calls:
	case <-time.After(time.Second):
		t.Fatal("short page did not silently continue")
	}
	cards := make([]ipc.SeriesCard, 20)
	for i := range cards {
		cards[i] = ipc.SeriesCard{ID: fmt.Sprint(i + 1), Title: fmt.Sprint(i + 1)}
	}
	p.pages <- ipc.DiscoverResponse{Items: cards, HasMore: true, Cursor: "after"}
	waitFor(time.Second, func() bool { tt.Frame(); return len(a.CatalogState().Items) == 21 })
	if len(a.CatalogState().Items) != 21 {
		t.Fatal("viewport was not filled")
	}
	for range 15 {
		tt.Frame()
		runtime.Gosched()
	}
	assertQuietLoading(t, tt)
	if len(p.calls) > 0 {
		t.Fatal("filled viewport kept fetching without a scroll")
	}
}

func TestEmptyCatalogPagesStopAndKeepContinuationRetryable(t *testing.T) {
	p := &scrollPageProvider{calls: make(chan string, 10), pages: make(chan ipc.DiscoverResponse, 10)}
	for i := range 3 {
		p.pages <- ipc.DiscoverResponse{HasMore: true, Cursor: fmt.Sprint(i + 1)}
	}
	a := NewTestApp()
	a.detailSource = p
	a.discoverFilterManual = true
	a.discoverFilterState = discoverCollapsed
	a.loadCatalog(false)
	tt := scrollInputTester(a, 1600, 1000)
	waitFor(time.Second, func() bool { tt.Frame(); return a.CatalogState().Error != "" })
	state := a.CatalogState()
	if state.Error == "" || state.Cursor != "3" || !state.HasMore || len(p.calls) != 3 {
		t.Fatalf("empty continuation did not stop safely: calls=%d state=%+v", len(p.calls), state)
	}
	p.pages <- ipc.DiscoverResponse{Items: []ipc.SeriesCard{{ID: "ready", Title: "有效内容"}}}
	tt.Frame()
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool { return len(a.CatalogState().Items) == 1 })
	tt.Frame()
	if a.CatalogState().Error != "" || !tt.HasText("有效内容") {
		t.Fatal("empty-page continuation could not recover")
	}
}
