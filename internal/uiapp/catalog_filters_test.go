package uiapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func officialPanels(t *testing.T) map[string][]ipc.FilterRowDTO {
	t.Helper()
	raw, err := os.ReadFile("../catalog/testdata/filter_panels.json")
	if err != nil {
		t.Fatal(err)
	}
	var panels map[string][]ipc.FilterRowDTO
	if err := json.Unmarshal(raw, &panels); err != nil {
		t.Fatal(err)
	}
	return panels
}

type taxonomyProvider struct {
	fakeDetail
	panels           map[string][]ipc.FilterRowDTO
	started, release chan struct{}
	blockedGenre     string
	filterError      error
	mu               sync.Mutex
	queries          []map[string]string
}

func (p *taxonomyProvider) GetFilterPanel(ctx context.Context, genre string) ([]ipc.FilterRowDTO, error) {
	if genre == p.blockedGenre && p.started != nil {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return p.panels[genre], p.filterError
}
func (p *taxonomyProvider) GetDiscover(_ context.Context, genre string, values map[string]string, cursor string) (ipc.DiscoverResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries = append(p.queries, values)
	return ipc.DiscoverResponse{Items: []ipc.SeriesCard{{ID: genre, Title: genre}}, Cursor: "next", HasMore: true}, nil
}

func TestOfficialTaxonomyDiffersByGenreAndChoicesStayIsolated(t *testing.T) {
	a := NewTestApp()
	a.detailSource = &taxonomyProvider{panels: officialPanels(t)}
	for _, c := range []struct {
		index, theme, role, epoch int
		style                     bool
	}{{0, 26, 80, 24, false}, {1, 26, 95, 34, true}, {2, 8, 8, 9, false}} {
		a.SelectGenre(c.index)
		a.loadFilters()
		rows := a.CatalogState().Filters
		counts := map[string]int{}
		for _, row := range rows {
			counts[row.Key] = len(row.Options) - 1
		}
		if counts["category_dim_theme"] != c.theme || counts["category_dim_role"] != c.role || counts["category_dim_epoch"] != c.epoch || (counts["category_dim_art_style"] > 0) != c.style {
			t.Fatalf("genre=%d wrong official rows: %v", c.index, counts)
		}
		for _, row := range rows {
			if row.Key == "category_dim_theme" {
				a.selectFilterValue(a.catalogGenre(), row.Key, row.Options[1].Value)
			}
		}
	}
	a.SelectGenre(0)
	values := catalogValues(a.catalogGenre(), a.catalog.Filters, a.catalog.Selection)
	if values["category_dim_theme"] != "cate_9165" || values["genre"] != "short_play" {
		t.Fatalf("lost human selection: %v", values)
	}
	a.selectFilterValue("ai_series", "category_dim_theme", "cate_316")
	if got := catalogValues(a.catalogGenre(), a.catalog.Filters, a.catalog.Selection); got["category_dim_theme"] != "cate_9165" {
		t.Fatal("delayed AI click changed human filters")
	}
	a.clearCatalogFilters()
	if values := catalogValues(a.catalogGenre(), a.catalog.Filters, a.catalog.Selection); len(values) != 1 {
		t.Fatalf("reset left selections: %v", values)
	}
}

func TestLateTaxonomyCannotReplaceCurrentGenre(t *testing.T) {
	p := &taxonomyProvider{panels: officialPanels(t), blockedGenre: "short_play", started: make(chan struct{}), release: make(chan struct{})}
	a := NewTestApp()
	a.detailSource = p
	done := make(chan struct{})
	go func() { a.loadFilters(); close(done) }()
	<-p.started
	a.SelectGenre(2)
	a.loadFilters()
	close(p.release)
	<-done
	state := a.CatalogState()
	if state.FiltersLoading || state.GenreIndex != 2 || len(state.Filters[0].Options) != 9 {
		t.Fatalf("stale taxonomy applied: %+v", state)
	}
}

func TestFilterFailureHasIndependentRetry(t *testing.T) {
	p := &taxonomyProvider{panels: officialPanels(t), filterError: errors.New("temporary")}
	a := NewTestApp()
	a.detailSource = p
	a.catalog.Items = []CatalogItem{{ID: "existing", Title: "已有短剧"}}
	a.loadFilters()
	tt := ui.NewTester(a.Render, 1280, 820)
	if !tt.HasText("已有短剧") || !tt.HasText("筛选条件暂时加载失败，请重试") || !tt.HasText("AI剧") {
		t.Fatal("panel failure hid content or genre control")
	}
	p.filterError = nil
	a.loadFilters()
	tt.Frame()
	if !a.CatalogState().FiltersLoaded || tt.HasText("筛选条件暂时加载失败，请重试") {
		t.Fatal("filter retry did not recover")
	}
}

func TestGenreResetCannotAppendAnOldResultPage(t *testing.T) {
	p := &taxonomyProvider{}
	a := NewTestApp()
	a.detailSource = p
	a.loadCatalog(false)
	a.SelectGenre(2)
	a.loadCatalog(true) // Layout must not win the race against first-page replacement.
	p.mu.Lock()
	n := len(p.queries)
	p.mu.Unlock()
	if n != 1 {
		t.Fatal("a reset was paginated before its replacement")
	}
	a.loadCatalog(false)
	state := a.CatalogState()
	if len(state.Items) != 1 || state.Items[0].ID != "ai_series" {
		t.Fatalf("mixed genres: %+v", state.Items)
	}
}

func TestNativeFilterChoiceSubmitsOfficialValueAndClears(t *testing.T) {
	a := NewTestApp()
	a.detailSource = &taxonomyProvider{panels: officialPanels(t)}
	a.SelectGenre(1)
	a.loadFilters()
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Advance(300 * time.Millisecond)
	if err := tt.Click("画风：3D漫"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool {
		state := a.CatalogState()
		return catalogValues("comic_series", state.Filters, state.Selection)["category_dim_art_style"] == "cate_5415"
	})
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if !tt.HasText("清除筛选") || !tt.HasText("筛选 · 漫剧 · 3D漫") {
		t.Fatal("selection summary missing")
	}
	if err := tt.Click("收起筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if !tt.HasText("真人剧") || !tt.HasText("AI剧") {
		t.Fatal("collapsing filters hid the genre switcher")
	}
	for _, index := range []int{2, 0, 1} {
		if err := tt.Click(a.catalog.Genre[index]); err != nil {
			t.Fatal(err)
		}
		if state := a.CatalogState(); state.GenreIndex != index {
			t.Fatalf("genre click was reordered or deferred: got %d want %d", state.GenreIndex, index)
		}
	}
}

func TestFilterNormalizationDoesNotDuplicateAllOrDropServerRows(t *testing.T) {
	rows := convertFilters([]ipc.FilterRowDTO{{Key: "genre", Name: "体裁", Options: []ipc.FilterOptionDTO{{Name: "真人剧", Value: "short_play"}}}, {Key: "future_row", Name: "全部新维度", Options: []ipc.FilterOptionDTO{{Name: "全部", Value: ""}, {Name: "官方选项", Value: "one"}, {Name: "重复", Value: "one"}}}})
	if len(rows) != 1 || rows[0].Title != "新维度" || len(rows[0].Options) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestSelectedFilterKeepsLabelAboveHighlight(t *testing.T) {
	selected := 0
	tt := ui.NewTester(func(c *ui.Context) {
		theme := hongguoTheme(c)
		c.SetTheme(&theme)
		ui.Box(c).Fill().Background(theme.Background).Children(func() {
			catalogChoice(c, ui.RadioBase(c, &selected, 0), "全部", true)
		})
	}, 100, 50)
	tt.Advance(300 * time.Millisecond)
	picture := tt.Image()
	light := 0
	for y := 0; y < picture.Bounds().Dy(); y++ {
		for x := 0; x < picture.Bounds().Dx(); x++ {
			pixel := picture.RGBAAt(x, y)
			if pixel.R > 180 && pixel.G > 180 && pixel.B > 180 {
				light++
			}
		}
	}
	if light < 10 {
		t.Fatalf("selected label was covered by the red layer: bright pixels=%d", light)
	}
}
