package uiapp

import (
	"context"
	"log"
	"strings"

	"myguo/internal/ipc"
)

type catalogFacet struct {
	rows      []FilterRow
	selection []int
}

// Called under mu, including by Render. Each genre owns its taxonomy and choices.
func (a *App) catalogGenre() string {
	genres := [...]string{"short_play", "comic_series", "ai_series"}
	if a.catalog.GenreIndex < 0 || a.catalog.GenreIndex >= len(genres) {
		return genres[0]
	}
	return genres[a.catalog.GenreIndex]
}

func catalogValues(genre string, rows []FilterRow, selected []int) map[string]string {
	values := map[string]string{"genre": genre}
	for i, row := range rows {
		if row.Key != "genre" && i < len(selected) && selected[i] >= 0 && selected[i] < len(row.Options) {
			if value := row.Options[selected[i]].Value; value != "" {
				values[row.Key] = value
			}
		}
	}
	return values
}

func (a *App) SelectGenre(next int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if next == a.catalog.GenreIndex || next < 0 || next >= len(a.catalog.Genre) {
		return
	}
	if a.catalogFacets == nil {
		a.catalogFacets = make(map[string]catalogFacet)
	}
	if a.catalog.FiltersLoaded {
		a.catalogFacets[a.catalogGenre()] = catalogFacet{a.catalog.Filters, append([]int(nil), a.catalog.Selection...)}
	}
	a.catalogFilterGeneration++
	a.catalog.GenreIndex = next
	stored, cached := a.catalogFacets[a.catalogGenre()]
	a.catalog.Filters, a.catalog.Selection = stored.rows, append([]int(nil), stored.selection...)
	a.catalog.FiltersLoaded, a.catalog.FiltersLoading, a.catalog.FilterError = cached, false, ""
	a.discoverFilterState, a.discoverFilterManual = discoverExpanded, false
	a.resetCatalog()
	if !a.testMode {
		a.startCatalogFiltersLocked()
		a.startCatalogResultsLocked()
	}
}

func (a *App) resetCatalog() {
	a.catalogGeneration++
	a.catalog.Cursor, a.catalog.Error = "", ""
	a.catalog.HasMore, a.catalog.Loading, a.catalog.LoadingMore = true, false, false
	a.catalog.resultsCurrent = false
	a.catalog.seenCursors = nil
	a.catalog.emptyPages = 0
	a.catalogScrollReset = true
	a.invalidate()
}

func (a *App) SelectFilter(rowIndex, optionIndex int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.selectCatalogFilterLocked(rowIndex, optionIndex)
}

// Bind a click to the genre and server values that were actually on screen.
// A delayed UI callback must not select a row at the same index in another genre.
func (a *App) selectFilterValue(genre, key, value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if genre != a.catalogGenre() {
		return
	}
	for i, row := range a.catalog.Filters {
		if row.Key != key {
			continue
		}
		for j, option := range row.Options {
			if option.Value == value {
				a.selectCatalogFilterLocked(i, j)
				return
			}
		}
	}
}

func (a *App) selectCatalogFilterLocked(rowIndex, optionIndex int) {
	if rowIndex < 0 || rowIndex >= len(a.catalog.Filters) || optionIndex < 0 || optionIndex >= len(a.catalog.Filters[rowIndex].Options) {
		return
	}
	selected := append([]int(nil), a.catalog.Selection...)
	for len(selected) < len(a.catalog.Filters) {
		selected = append(selected, 0)
	}
	if selected[rowIndex] == optionIndex {
		return
	}
	selected[rowIndex] = optionIndex
	a.catalog.Selection = selected
	a.resetCatalog()
	if !a.testMode {
		a.startCatalogResultsLocked()
	}
}

func (a *App) clearCatalogFilters() {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for _, i := range a.catalog.Selection {
		changed = changed || i != 0
	}
	if !changed {
		return
	}
	a.catalog.Selection = make([]int, len(a.catalog.Filters))
	a.resetCatalog()
	if !a.testMode {
		a.startCatalogResultsLocked()
	}
}

type filterRequest struct {
	genre      string
	generation uint64
	provider   detailProvider
}

func (a *App) prepareCatalogFiltersLocked() *filterRequest {
	provider := a.detailProvider()
	if provider == nil || a.catalog.FiltersLoading || a.catalog.FiltersLoaded {
		return nil
	}
	a.catalog.FiltersLoading, a.catalog.FilterError = true, ""
	a.invalidate()
	return &filterRequest{a.catalogGenre(), a.catalogFilterGeneration, provider}
}
func (a *App) startCatalogFiltersLocked() {
	if request := a.prepareCatalogFiltersLocked(); request != nil {
		go a.fetchCatalogFilters(*request)
	}
}
func (a *App) loadFilters() {
	a.mu.Lock()
	request := a.prepareCatalogFiltersLocked()
	a.mu.Unlock()
	if request != nil {
		a.fetchCatalogFilters(*request)
	}
}
func (a *App) fetchCatalogFilters(request filterRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	rows, err := request.provider.GetFilterPanel(ctx, request.genre)
	a.mu.Lock()
	defer a.mu.Unlock()
	if request.generation != a.catalogFilterGeneration || request.genre != a.catalogGenre() {
		return
	}
	a.catalog.FiltersLoading = false
	if err != nil {
		log.Printf("[catalog] filter panel %s: %v", request.genre, err)
		a.catalog.FilterError = "筛选条件暂时加载失败，请重试"
	} else {
		a.catalog.Filters = convertFilters(rows)
		a.catalog.Selection = make([]int, len(a.catalog.Filters))
		a.catalog.FiltersLoaded, a.catalog.FilterError = true, ""
	}
	a.invalidate()
}

type catalogRequest struct {
	genre, cursor string
	generation    uint64
	values        map[string]string
	more          bool
	provider      detailProvider
}

func (a *App) prepareCatalogResultsLocked(more bool) *catalogRequest {
	provider := a.detailProvider()
	if provider == nil || a.catalog.Loading || a.catalog.LoadingMore || (more && (!a.catalog.HasMore || !a.catalog.resultsCurrent)) {
		return nil
	}
	cursor := ""
	if more {
		cursor = a.catalog.Cursor
	}
	a.catalog.Loading, a.catalog.LoadingMore, a.catalog.Error = !more, more, ""
	a.invalidate()
	return &catalogRequest{a.catalogGenre(), cursor, a.catalogGeneration, catalogValues(a.catalogGenre(), a.catalog.Filters, a.catalog.Selection), more, provider}
}
func (a *App) startCatalogResultsLocked() {
	if request := a.prepareCatalogResultsLocked(false); request != nil {
		go a.fetchCatalogResults(*request)
	}
}
func (a *App) loadCatalog(more bool) {
	a.mu.Lock()
	request := a.prepareCatalogResultsLocked(more)
	a.mu.Unlock()
	if request != nil {
		a.fetchCatalogResults(*request)
	}
}
func (a *App) fetchCatalogResults(request catalogRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	page, err := request.provider.GetDiscover(ctx, request.genre, request.values, request.cursor)
	a.mu.Lock()
	defer a.mu.Unlock()
	if request.generation != a.catalogGeneration {
		return
	}
	a.catalog.Loading, a.catalog.LoadingMore = false, false
	if err != nil {
		log.Printf("[catalog] results %s: %v", request.genre, err)
		a.catalog.Error = "目录加载失败，请稍后重试"
		a.invalidate()
		return
	}
	priorCount := 0
	if request.more {
		priorCount = len(a.catalog.Items)
	} else {
		a.catalog.emptyPages = 0
	}
	items := convertCards(page.Items)
	if request.more {
		items = append(append([]CatalogItem(nil), a.catalog.Items...), items...)
	}
	a.catalog.Items = dedupeCards(items)
	a.catalog.Cursor, a.catalog.HasMore = page.Cursor, nextCursor(&a.catalog.seenCursors, request.cursor, page.Cursor, page.HasMore, request.more)
	if a.catalog.HasMore && len(a.catalog.Items) == priorCount {
		a.catalog.emptyPages++
	} else {
		a.catalog.emptyPages = 0
	}
	if a.catalog.emptyPages >= maxEmptyPages {
		a.catalog.Error = "暂时无法取得更多内容，请重试"
	}
	a.catalog.resultsCurrent, a.catalog.resultGenre = true, request.genre
	if !request.more {
		a.catalogResultVersion++
	}
	a.invalidate()
}

func convertFilters(rows []ipc.FilterRowDTO) []FilterRow {
	out := make([]FilterRow, 0, len(rows))
	seenRows := make(map[string]bool)
	for _, row := range rows {
		if row.Key == "" || row.Key == "genre" || seenRows[row.Key] {
			continue
		}
		options := []FilterOption{{Label: "全部", Value: ""}}
		seen := map[string]bool{"": true}
		for _, option := range row.Options {
			if seen[option.Value] || strings.TrimSpace(option.Name) == "" {
				continue
			}
			seen[option.Value] = true
			options = append(options, FilterOption{Label: option.Name, Value: option.Value})
		}
		if len(options) == 1 {
			continue
		}
		seenRows[row.Key] = true
		title := strings.TrimPrefix(strings.TrimSpace(row.Name), "全部")
		if title == "" {
			title = row.Key
		}
		out = append(out, FilterRow{Title: title, Key: row.Key, Options: options})
	}
	return out
}
