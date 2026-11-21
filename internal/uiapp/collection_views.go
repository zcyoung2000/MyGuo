package uiapp

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// posterRow is a row inside a native List, which retains its item anchor as
// pages append and row heights are measured. Virtual rows do not animate their
// layout positions: scrolling owns that motion (MyGo v0.9.1).
func (a *App) posterRow(c *ui.Context, items []CatalogItem, width float32) {
	cardWidth := dramaCardWidth(width)
	height := a.collectionRowHeight(c, cardWidth, items) + cardWidth*posterAspectRatio
	ui.Row(c).FillWidth().Gap(dramaCardGap).Height(height).Children(func() {
		for _, item := range items {
			a.fixedHeightCatalogCardSized(c, item, cardWidth, height)
		}
	})
}

// cardGrid gives posters the full column width and preserves their 3:4 ratio.
// Read the host's last layout, just as GridView does when fitting its columns.
func (a *App) sharedCardGrid(c *ui.Context, key string, state *ui.GridState, scroll *ui.ScrollState, items []CatalogItem, extra float32, after func(CatalogItem)) ui.Element {
	host := ui.Column(c.Key(key)).MinWidth(0).MinHeight(0).Transition(resultTransition)
	width := host.Bounds().W
	baseTheme := *c.Theme()
	gridTheme := baseTheme
	// GridView uses Space(2) for both axes. Match its visible poster gap to list rows.
	gridTheme.Spacing = dramaCardGap / 2
	gap := gridTheme.Space(2)
	columns := max(1, int((width+gap)/(dramaMinCardWidth+gap)))
	cardWidth := (width - float32(columns-1)*gap) / float32(columns)
	configureDramaGrid(state, items)
	textHeight := a.collectionRowHeight(c, cardWidth, items)
	// GridView adds half-gap padding above and below each row. Account for its
	// cell border and fractional text measurements so the visible gutter stays
	// aligned with list rows at 20 DIPs.
	height := textHeight + cardWidth*posterAspectRatio + max(0, cardGridVerticalGap-gap) + 2*gridCellVerticalInset + gridCellHeightRounding + extra
	host.Children(func() {
		c.SetTheme(&gridTheme)
		defer c.SetTheme(&baseTheme)
		grid := ui.GridView(c.Key(fmt.Sprintf("result:%d", a.collectionVersion())), state, len(items), dramaMinCardWidth, height, func(index int) {
			c.SetTheme(&baseTheme)
			defer c.SetTheme(&gridTheme)
			item := items[index]
			a.fixedHeightCatalogCardSized(c, item, cardWidth, height)
			if after != nil {
				after(item)
			}
		})
		grid.Scrollbars(false).Transition(resultTransition)
		if scroll == nil {
			grid.Grow(1)
		} else {
			grid.TrackScroll(scroll).ScrollbarInsets(0, 0, 0, 0)
		}
	})
	return host
}

func (a *App) collectionVersion() uint64 {
	switch a.view {
	case "search":
		return a.searchResultVersion
	case "new":
		return a.releaseResultVersion
	case "history":
		return a.libraryResultVersion[libraryHistory]
	case "favorites":
		return a.libraryResultVersion[libraryFavorites]
	case "likes":
		return a.libraryResultVersion[libraryLikes]
	case "reservations":
		return a.libraryResultVersion[libraryReservations]
	}
	return 0
}

const (
	catalogOverlayHeight           float32 = 30
	catalogOverlayInset            float32 = 8
	catalogTextLineHeight          float32 = 20
	catalogRowGap                  float32 = 6
	catalogMetadataLineHeight      float32 = 16
	catalogReservationButtonHeight float32 = 28
	gridCellVerticalInset          float32 = 2
	gridCellHeightRounding         float32 = 1
)

func (a *App) collectionRowHeight(c *ui.Context, cardWidth float32, items []CatalogItem) float32 {
	var tallest float32
	for _, item := range items {
		height := measuredTitleHeight(c, cardWidth, item.Title)
		switch a.view {
		case "history":
			height += catalogRowGap + measuredTextHeight(c, cardWidth, formatRelativeTime(item.UpdatedAt), 12, 400)
		case "reservations":
			if item.PublishAt != "" {
				height += catalogRowGap + measuredTextHeight(c, cardWidth, formatPublishDate(item.PublishAt), 12, 400)
			}
		case "new":
			if item.Upcoming {
				if item.PublishAt != "" {
					height += catalogRowGap + measuredTextHeight(c, cardWidth, formatPublishDate(item.PublishAt), 12, 400)
				}
				height += catalogRowGap + catalogReservationButtonHeight
			} else {
				if item.Heat != "" {
					height += catalogRowGap + measuredTextHeight(c, cardWidth, item.Heat, 12, 700)
				}
			}
		default:
			if item.Upcoming {
				if item.PublishAt != "" && a.view != "favorites" && a.view != "likes" {
					height += catalogRowGap + measuredTextHeight(c, cardWidth, formatPublishDate(item.PublishAt), 13, 400)
				}
				if a.view != "favorites" && a.view != "likes" && a.view != "reservations" {
					height += catalogRowGap + catalogReservationButtonHeight
				}
			} else {
				if a.view != "search" && a.view != "favorites" && a.view != "likes" && item.BadgeText != "" {
					height += catalogRowGap + measuredTextHeight(c, cardWidth, item.BadgeText, c.Theme().FontSize, 400)
				}
				if a.view != "search" && a.view != "detail" && a.view != "favorites" && a.view != "likes" && item.Category != "" {
					height += catalogRowGap + measuredTextHeight(c, cardWidth, item.Category, 13, 400)
				}
				if a.view != "favorites" && a.view != "likes" && (item.Heat != "" || item.EpisodeCount > 0) {
					meta := item.Heat
					if item.EpisodeCount > 0 {
						meta = fmt.Sprintf("%d集", item.EpisodeCount)
						if item.Heat != "" {
							meta += " · " + item.Heat
						}
					}
					height += catalogRowGap + measuredTextHeight(c, cardWidth, meta, c.Theme().FontSize, 400)
				}
			}
		}
		tallest = max(tallest, height)
	}
	return tallest
}

func measuredTitleHeight(c *ui.Context, width float32, title string) float32 {
	_, height := c.MeasureText(width, ui.Span{Text: title, Size: 13, Weight: 700})
	return max(catalogTextLineHeight, height)
}

func measuredTextHeight(c *ui.Context, width float32, text string, size float32, weight ...int) float32 {
	if text == "" {
		return 0
	}
	spanWeight := 400
	if len(weight) > 0 {
		spanWeight = weight[0]
	}
	_, height := c.MeasureText(width, ui.Span{Text: text, Size: size, Weight: spanWeight})
	return max(float32(1), height)
}
