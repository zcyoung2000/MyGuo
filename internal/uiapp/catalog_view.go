package uiapp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) renderCatalog(c *ui.Context) {
	catalog := a.catalog
	a.updateDiscoverFiltersOnScroll(a.discoverPageScroll.Y)
	a.renderCatalogHeader(c)
	if a.catalogList == nil {
		a.catalogList = &ui.ListState{}
	}
	width := a.gridContentWidth(c)
	columns := dramaColumns(width)
	rows := (len(catalog.Items) + columns - 1) / columns
	state := a.catalogList
	state.Key = func(row int) any {
		if row == 0 {
			return "catalog-filters"
		}
		if row > rows {
			return "catalog-footer"
		}
		item := catalog.Items[(row-1)*columns]
		return item.ID + "|" + item.EpisodeID
	}
	state.NearEnd = 2
	state.EndReached = func(int) {
		if catalog.resultsCurrent && catalog.HasMore && !catalog.Loading && !catalog.LoadingMore && catalog.Error == "" {
			go a.LoadMoreCatalog()
		}
	}
	ui.List(c.Key("discover-page-scroll"), state, rows+2, func(row int) {
		if row == 0 {
			a.renderCatalogControls(c, catalog)
			return
		}
		if row <= rows {
			first := (row - 1) * columns
			a.posterRow(c, catalog.Items[first:min(first+columns, len(catalog.Items))], width)
			return
		}
		if catalog.Error != "" {
			inlineError(c, catalog.Error, func() { go a.loadCatalog(catalog.Cursor != "") })
		} else if len(catalog.Items) == 0 {
			if catalog.Loading {
				a.discoverSkeleton(c, width)
			} else if !catalog.HasMore {
				a.emptyView(c, "暂无短剧", "换个筛选条件试试")
			}
		}

		a.continuePageInView(c, "catalog-page-end", pageFillVersion{a.catalogGenre(), catalog.Cursor, len(catalog.Items)}, 0,
			a.discoverPageScroll.MaxY <= 0 && catalog.resultsCurrent && catalog.HasMore && !catalog.Loading && !catalog.LoadingMore && catalog.Error == "", a.LoadMoreCatalog)

	}).Grow(1).Gap(cardGridVerticalGap).Scrollbars(false).TrackScroll(&a.discoverPageScroll).Label("discover-page-scroll")
}

func (a *App) renderCatalogHeader(c *ui.Context) {
	// The genre stays reachable even when the detailed conditions are folded.
	ui.Row(c.Key("catalog-genre-toolbar")).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		tabs := make([]slidingTabItem, len(a.catalog.Genre))
		for i, label := range a.catalog.Genre {
			tabs[i] = slidingTabItem{Key: strconv.Itoa(i), Label: label}
		}
		a.renderSlidingTabs(c, "catalog-genres", "catalog-genres", tabs, strconv.Itoa(a.catalog.GenreIndex), func(key string) {
			index, err := strconv.Atoi(key)
			if err == nil {
				a.SelectGenre(index)
			}
		})
	})
	a.renderSearchBar(c)
}

func (a *App) catalogFilterSummary(catalog Catalog) string {
	var selected []string
	if catalog.GenreIndex >= 0 && catalog.GenreIndex < len(catalog.Genre) {
		selected = append(selected, catalog.Genre[catalog.GenreIndex])
	}
	for rowIndex, row := range catalog.Filters {
		if rowIndex >= len(catalog.Selection) {
			continue
		}
		optionIndex := catalog.Selection[rowIndex]
		if optionIndex <= 0 || optionIndex >= len(row.Options) {
			continue
		}
		selected = append(selected, row.Options[optionIndex].Label)
	}
	if len(selected) == 0 {
		return "筛选"
	}
	return "筛选 · " + strings.Join(selected, " · ")
}

func (a *App) discoverSkeleton(c *ui.Context, width float32) {
	cardWidth := dramaCardWidth(width)
	rowHeight := cardWidth*posterAspectRatio + catalogTextLineHeight + catalogMetadataLineHeight + 2*catalogRowGap
	posterGridSkeleton(c, 12, width, func(float32) float32 { return rowHeight })
}

const (
	discoverFilterCollapseThreshold float32 = 48
	discoverFilterExpandThreshold   float32 = 4
)

func (a *App) updateDiscoverFiltersOnScroll(y float32) {
	if a.discoverFilterManual {
		return
	}
	if y > discoverFilterCollapseThreshold {
		a.discoverFilterState = discoverCollapsed
	} else if y <= discoverFilterExpandThreshold {
		a.discoverFilterState = discoverExpanded
	}
}

func (a *App) renderCatalogControls(c *ui.Context, catalog Catalog) ui.Element {
	open := a.discoverFilterState != discoverCollapsed
	card := contentSurface(ui.Column(c.Key("catalog-filter-card")).Padding(10, 14).Radius(14))
	card.Children(func() {
		parts := ui.CollapsibleBase(c.Key("catalog-filter-header"), &open)
		parts.Trigger.FillWidth().Height(34).Padding(0, 10).Radius(8).AlignItems(ui.Center).Gap(8).
			Label(chooseText(open, "收起筛选", "展开筛选")).OnChange(func() {
			a.discoverFilterManual = true
			a.discoverFilterState = discoverCollapsed
			if open {
				a.discoverFilterState = discoverExpanded
			}
			a.invalidate()
		}).Children(func() {
			ui.Text(c, a.catalogFilterSummary(catalog)).FontSize(13).Bold().SingleLine().Grow(1).TextColor(ui.RGBA(255, 255, 255, .9))
			ui.Text(c, chooseText(open, "收起", "展开")).FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Icon(c, playerDownIcon).Size(14, 14).Rotate(180 * parts.Progress()).TextColor(c.Theme().TextMuted)
		})
		if open || parts.Progress() > 0 {
			parts.Panel(func() {
				ui.Column(c.Key("catalog-filter-content:"+a.catalogGenre())).Padding(4, 0, 0, 0).Gap(12).Transition(resultTransition).Children(func() {
					if catalog.FiltersLoading {
						a.filterSkeleton(c)
					} else if catalog.FilterError != "" {
						inlineError(c, catalog.FilterError, func() { go a.loadFilters() })
					} else {
						a.renderFilterRows(c, catalog)
					}
					for _, index := range catalog.Selection {
						if index > 0 {
							panelAction(c, "catalog-reset", "清除筛选", a.clearCatalogFilters)
							break
						}
					}
				})
			})
		}
	})
	return card
}

func (a *App) renderFilterRows(c *ui.Context, catalog Catalog) {
	genre := a.catalogGenre()
	for rowIndex, row := range catalog.Filters {
		rIdx, rTitle, options := rowIndex, row.Title, row.Options
		selected := 0
		if rIdx < len(catalog.Selection) {
			selected = catalog.Selection[rIdx]
		}
		rowKey := fmt.Sprintf("%d:%s:%s", catalog.GenreIndex, row.Key, rTitle)
		expanded := a.expandedFilters[rowKey]
		limited := len(options) > 10
		renderOption := func(index int) {
			opt := options[index]
			chip := ui.RadioBase(c.Key("filter-option:"+row.Key+":"+opt.Value), &selected, index).
				Label(rTitle + "：" + opt.Label).OnChange(func() { a.selectFilterValue(genre, row.Key, opt.Value) })
			catalogChoice(c, chip, opt.Label, index == selected)
		}
		ui.Row(c.Key("filter-row:" + rowKey)).Gap(12).AlignItems(ui.Start).Children(func() {
			ui.Text(c, rTitle).FontSize(12).TextColor(c.Theme().TextMuted).Width(40).Padding(5, 0, 0, 0)
			ui.RadioGroup(c.Key("filter-options:"+rowKey), func() {
				var more ui.CollapsibleParts
				ui.Row(c).Wrap().Gap(8).Children(func() {
					visible := 0
					for i := range options {
						if !limited || i < 8 || i == selected {
							renderOption(i)
							visible++
						}
					}
					if limited {
						more = ui.CollapsibleBase(c.Key("filter-more:"+rowKey), &expanded)
						label := fmt.Sprintf("更多 %d 项", len(options)-visible)
						if expanded {
							label = "收起"
						}
						trigger := more.Trigger.Height(28).Padding(4, 10).Radius(14).AlignItems(ui.Center).
							Label(chooseText(expanded, "收起", "展开") + rTitle + "筛选").OnChange(func() {
							if a.expandedFilters == nil {
								a.expandedFilters = make(map[string]bool)
							}
							a.expandedFilters[rowKey] = expanded
							a.invalidate()
						})
						hoverBackground(trigger, ui.RGBA(255, 255, 255, .04), ui.RGBA(255, 255, 255, .08))
						trigger.Children(func() { ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted) })
					}
				})
				if limited && (expanded || more.Progress() > 0) {
					more.Panel(func() {
						ui.Row(c).Wrap().Gap(8).Children(func() {
							for i := range options {
								if i >= 8 && i != selected {
									renderOption(i)
								}
							}
						})
					})
				}
			}).Grow(1).Gap(8).Label(rTitle + "筛选")
		})
	}
}

// MyGo v0.9.0 RadioBase/RadioGroup provide choice semantics and keyboard
// navigation; the shared page material supplies our dark appearance. The
// selected layer fades without changing text weight, geometry or layout.
func catalogChoice(c *ui.Context, chip ui.Element, label string, selected bool) {
	chip.Height(28).Padding(4, 14).Radius(14).AlignItems(ui.Center).Justify(ui.Center).
		Material(pageChromeMaterial()).Border(1, ui.RGBA(255, 255, 255, .10))
	hoverBackground(chip, ui.Transparent, ui.RGBA(255, 255, 255, .06))
	chip.Children(func() {
		fade := float32(1)
		if selected {
			fade = 0
		}
		ui.Box(c.Key("selected")).Absolute().Fill().Radius(14).PassThrough().
			LinearGradient(selectionFill()).
			AnimatePose(ui.Pose{Fade: fade}, selectionAnimation)
		ui.Text(c, label).ZIndex(1).FontSize(12).SingleLine().TextColor(ui.RGBA(255, 255, 255, .90))
	})
}
