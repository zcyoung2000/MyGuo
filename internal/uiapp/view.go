package uiapp

import (
	_ "embed"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

//go:embed icons/discover.svg
var discoverSVG []byte

//go:embed icons/search.svg
var searchSVG []byte

//go:embed icons/history.svg
var historySVG []byte

//go:embed icons/favorite.svg
var favoriteSVG []byte

//go:embed icons/reservation.svg
var reservationSVG []byte

//go:embed icons/recommend.svg
var recommendSVG []byte

//go:embed icons/like.svg
var likeSVG []byte

//go:embed icons/rank.svg
var rankSVG []byte

//go:embed icons/new.svg
var newSVG []byte

//go:embed icons/settings.svg
var settingsSVG []byte

//go:embed icons/episodes.svg
var episodesSVG []byte

//go:embed icons/close.svg
var closeSVG []byte

var (
	rankIcon        = ui.MustParseSVG(rankSVG)
	newIcon         = ui.MustParseSVG(newSVG)
	settingsIcon    = ui.MustParseSVG(settingsSVG)
	likeIcon        = ui.MustParseSVG(likeSVG)
	recommendIcon   = ui.MustParseSVG(recommendSVG)
	discoverIcon    = ui.MustParseSVG(discoverSVG)
	searchIcon      = ui.MustParseSVG(searchSVG)
	historyIcon     = ui.MustParseSVG(historySVG)
	favoriteIcon    = ui.MustParseSVG(favoriteSVG)
	reservationIcon = ui.MustParseSVG(reservationSVG)
	episodesIcon    = ui.MustParseSVG(episodesSVG)
	closeIcon       = ui.MustParseSVG(closeSVG)
)

func (a *App) Render(c *ui.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// The framework also reads tracked scroll state after Render returns. Reset
	// it on this UI thread, never from a background navigation request.
	if a.detailScrollReset {
		a.detailScroll = ui.ScrollState{}
		a.detailScrollReset = false
	}
	if a.catalogScrollReset {
		a.discoverPageScroll.Y = 0
		a.catalogScrollReset = false
	}
	if a.rankScrollReset {
		a.rankScroll = ui.ScrollState{}
		a.rankScrollReset = false
	}
	a.beginArtworkFrame()
	theme := hongguoTheme(c)
	c.SetTheme(&theme)
	// Expose the system sidebar material directly, as in MyGo Music. The
	// content pane paints its own opaque background; accessibility keeps a fallback.
	c.Root().Background(windowGroundColor(c.Vibrancy() && !c.Preferences().ReduceTransparency))
	width := a.sidebarWidth
	if width == 0 {
		width = defaultSidebarWidth
	}
	shell := ui.Box(c.Key("app-shell")).Fill()
	shell.Children(func() {
		ui.Column(c.Key("app-shell-layout")).Fill().MinWidth(0).MinHeight(0).Children(func() {
			if a.playerFullscreen && a.hostedScene() {
				a.renderBody(c)
				return
			}
			bar := c.TitleBar()
			if height := windowDragRegionHeight(bar); height > 0 {
				ui.Row(c.Key("window-drag-region")).Label("拖动窗口").FillWidth().Height(height).
					Padding(0, bar.Right, 0, bar.Left).DragWindow()
			}
			ui.Split(c, &width, func() { a.renderSidebar(c) }, func() { a.renderBody(c) }).
				Grow(1).FillWidth().MinHeight(0).OnChange(func() { a.SetSidebarWidth(width) })
		})
	})
	prefs := c.Preferences()
	if c.Vibrancy() && !prefs.ReduceTransparency && !prefs.HighContrast && !a.hostedScene() && !a.playerFixture {
		shell.Effect(sidebarBleedEffect{width: width})
	}
}

func windowDragRegionHeight(bar ui.TitleBar) float32 {
	if bar.Left > 0 || bar.Right <= 0 {
		return 0
	}
	if bar.Height > 32 {
		return bar.Height
	}
	return 32
}

// sidebarTopInset is the room the sidebar keeps above its content for the
// window controls, when they sit over the sidebar: the band macOS draws its
// traffic lights in, which ends at their bottom edge. Windows and Linux draw
// their window buttons in the top-right corner, where the sidebar is not, so
// the sidebar keeps no room for them and its content starts at the top edge.
// Full screen hides the controls on every platform, and the band with them.
func sidebarTopInset(bar ui.TitleBar) float32 {
	if bar.Left <= 0 {
		return 0
	}
	return bar.Bottom
}

// hongguoTheme defines the app's shared native palette:
// near-black surfaces, the Hongguo red accent and compact control spacing.
func hongguoTheme(c *ui.Context) ui.Theme {
	t := *c.Theme()
	t.Dark = true
	t.Background = ui.Hex("#0e0f14")
	t.Surface = ui.Hex("#14151c")
	t.SurfaceHover = ui.Hex("#1e202a")
	t.SurfacePressed = ui.Hex("#262938")
	t.Border = ui.RGBA(255, 255, 255, 0.08)
	t.Text = ui.Hex("#ffffff")
	t.TextMuted = ui.RGBA(255, 255, 255, 0.65)
	t.Accent = ui.Hex("#e21a44")
	t.AccentHover = ui.Hex("#fe2c55")
	t.AccentPressed = ui.Hex("#c31639")
	t.AccentText = ui.Hex("#ffffff")
	t.Danger = ui.Hex("#ff4d5f")
	t.Selection = ui.RGBA(226, 26, 68, 0.28)
	t.Focus = ui.RGBA(254, 44, 85, 0.58)
	t.Inverse = ui.Hex("#262938")
	t.InverseText = ui.Hex("#ffffff")
	t.Radius = 8
	t.Spacing = 3
	return t
}
func (a *App) renderSidebar(c *ui.Context) {
	top := sidebarTopInset(c.TitleBar())
	sidebar := ui.Box(c.Key("sidebar-container")).Fill()
	a.renderPlayerSidebarAmbient(c, sidebar)
	sidebar.Children(func() {
		ui.Scroll(c.Key("sidebar")).Scrollbars(false).Fill().Padding(0, 10, 16, 10).Gap(2).Children(func() {
			// Draggable room for the window controls (no brand text): the
			// traffic lights on macOS, and nothing above the sidebar content
			// on Windows and Linux.
			ui.Box(c.Key("sidebar-traffic-lights")).Height(top).DragWindow()

			// 发现 Section
			ui.Text(c, "发现").FontSize(11).Bold().TextColor(ui.RGBA(255, 255, 255, 0.48)).Padding(10, 10, 4).LetterSpacing(0.6)
			a.sidebarItem(c, "recommend", "推荐", recommendIcon)
			a.sidebarItem(c, "discover", "找剧", discoverIcon)
			a.sidebarItem(c, "rank", "排行榜", rankIcon)
			a.sidebarItem(c, "new", "新剧", newIcon)

			// 我的 Section
			ui.Text(c, "我的").FontSize(11).Bold().TextColor(ui.RGBA(255, 255, 255, 0.48)).Padding(14, 10, 4).LetterSpacing(0.6)
			a.sidebarItem(c, "history", "历史", historyIcon)
			a.sidebarItem(c, "favorites", "收藏", favoriteIcon)
			a.sidebarItem(c, "reservations", "预约", reservationIcon)
			a.sidebarItem(c, "settings", "设置", settingsIcon)
		})
	})
}
func (a *App) hostedScene() bool {
	return a.media != nil && (a.mediaOn || a.view == "recommend")
}

func (a *App) renderBody(c *ui.Context) {
	// Keep this parent stable, but give native pages and the media surface distinct
	// identities. Removing the native media surface reveals the next page.
	ui.Column(c.Key("content-host")).Fill().MinWidth(0).MinHeight(0).Background(c.Theme().Background).Children(func() {
		if a.playerFixture {
			ui.Column(c.Key("fixture-frame")).Fill().Children(func() {
				a.renderPlayerVisualFixture(c)
			})
			return
		}
		if a.hostedScene() {
			page := ui.Column(c.Key("hosted-page")).Fill().MinWidth(0).MinHeight(0)
			page.Transition(hostedPageTransition).Children(func() {
				a.renderNativePlayer(c)
			})
			if a.view == "recommend" && !a.mediaOn {
				a.renderFeedChannels(c)
			}
			return
		}
		a.renderPageNavigation(c)
		page := ui.Column(c.Key("native-page:"+a.view+":"+a.seriesID)).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Padding(8, 16, 12, 16).Gap(10)
		page.Transition(pageTransition).Children(func() {
			if a.error != "" {
				ui.Text(c, a.error).TextColor(c.Theme().Danger)
			}
			if a.notice != "" {
				ui.Text(c, a.notice).TextColor(c.Theme().Accent)
			}
			switch a.view {
			case "settings":
				a.renderSettings(c)
			case "search":
				a.renderSearch(c)
			case "history":
				a.renderLibrary(c, libraryHistory)
			case "favorites":
				a.renderLibrary(c, libraryFavorites)
			case "likes":
				a.renderLibrary(c, libraryLikes)
			case "reservations":
				a.refreshVisibleLibraryLocked(libraryReservations)
				a.renderLibrary(c, libraryReservations)
			case "detail":
				a.renderDetail(c)
			case "rank":
				a.renderRank(c)
			case "new":
				a.renderReleases(c)
			case "actor":
				a.renderActor(c)
			default:
				a.renderCatalog(c)
			}
		})
	})
}

func (a *App) sidebarItem(c *ui.Context, id, label string, icon *ui.SVG) {
	selected := a.view == id
	row := ui.Row(c.Key(id)).Height(36).Role(ui.RoleButton).Label(label).Focusable().
		Padding(8, 12).Radius(8).Gap(11).AlignItems(ui.Center).
		Transition(selectionTransition)

	if selected {
		row.LinearGradient(ui.LinearGradient{
			From:  ui.Hex("#d91b42"),
			To:    ui.Hex("#b81235"),
			Angle: 135,
		}).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.35))
	} else {
		hoverBackground(row, ui.Transparent, ui.RGBA(255, 255, 255, 0.08))
	}

	if row.Clicked() || row.Shortcut(0, ui.KeyEnter) || row.Shortcut(0, ui.KeySpace) {
		go a.OpenView(id)
	}

	row.Children(func() {
		ic := ui.Icon(c, icon).Size(17, 17)
		txt := ui.Text(c, label).FontSize(13)
		if selected {
			ic.TextColor(ui.Hex("#ffffff"))
			txt.Bold().TextColor(ui.Hex("#ffffff"))
		} else if row.Hovered() {
			ic.TextColor(ui.Hex("#ffffff"))
			txt.TextColor(ui.Hex("#ffffff"))
		} else {
			ic.TextColor(ui.RGBA(255, 255, 255, 0.72))
			txt.TextColor(ui.RGBA(255, 255, 255, 0.82))
		}
	})
}

func (a *App) pillChip(c *ui.Context, key, label string, selected bool, onClick func()) {
	chip := ui.Row(c.Key(key)).Height(28).Padding(4, 14).Radius(14).
		Role(ui.RoleButton).Label(label).Focusable().AlignItems(ui.Center).Justify(ui.Center).
		Transition(selectionTransition)
	if selected {
		chip.LinearGradient(selectionFill()).Border(1, ui.RGBA(255, 255, 255, 0.22)).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.35))
	} else {
		chip.Material(pageChromeMaterial()).Border(1, ui.RGBA(255, 255, 255, 0.08))
		hoverBackground(chip, ui.Transparent, ui.Transparent)
	}
	if chip.Clicked() || chip.Shortcut(0, ui.KeyEnter) || chip.Shortcut(0, ui.KeySpace) {
		if onClick != nil {
			onClick()
		}
	}
	chip.Children(func() {
		txt := ui.Text(c, label).FontSize(12)
		if selected {
			txt.Bold().TextColor(ui.Hex("#ffffff"))
		} else if chip.Hovered() {
			txt.TextColor(ui.Hex("#ffffff"))
		} else {
			txt.TextColor(ui.RGBA(255, 255, 255, 0.75))
		}
	})
}

type slidingTabMatch struct{ group string }

type slidingTabItem struct {
	Key   string
	Label string
}

func (a *App) renderSlidingTabs(c *ui.Context, key, group string, tabs []slidingTabItem, selectedKey string, onSelect func(string)) {
	selectedIndex := 0
	for i, tab := range tabs {
		if tab.Key == selectedKey {
			selectedIndex = i
		}
	}
	parts := ui.SegmentedBase(c.Key(key), &selectedIndex, len(tabs))
	track := parts.Track.Label("列表筛选").AlignSelf(ui.Start).Padding(4, 6).Gap(4).Radius(24).
		Material(pageChromeMaterial()).
		Border(1, ui.RGBA(255, 255, 255, 0.12)).
		AlignItems(ui.Center).OnChange(func() {
		if onSelect != nil {
			onSelect(tabs[selectedIndex].Key)
		}
	})
	track.Children(func() {
		for index, tab := range tabs {
			tKey, tLabel := tab.Key, tab.Label
			selected := selectedKey == tKey
			chip := parts.Segment(index).Height(28).Padding(0, 16).Radius(18).
				Label(tLabel).AlignItems(ui.Center).Justify(ui.Center)
			if !selected {
				hoverBackground(chip, ui.Transparent, ui.RGBA(255, 255, 255, 0.08))
			}
			chip.Children(func() {
				// 1. In-flow sizer: guarantees chip maintains its natural width without collapsing
				ui.Row(c.Key("sizer:" + group + ":" + tKey)).Opacity(0).Children(func() {
					ui.Text(c, tLabel).FontSize(12).Bold().SingleLine()
				})
				// 2. Pure background sliding glider: slides across tabs without moving the text
				if selected {
					ui.Box(c.Key("sliding-pill:"+group)).Absolute().Fill().Radius(18).
						LinearGradient(selectionFill()).Shadow(0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.35)).
						Transition(ui.ElementTransition{
							Duration: selectionTransition.Duration,
							Ease:     selectionTransition.Ease,
							Match:    slidingTabMatch{group: group},
						})
				}
				// 3. Stationary text label: stays in place over the glider, only cross-fading color
				ui.Row(c.Key("tab-label:" + group + ":" + tKey)).Absolute().Fill().
					AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					txt := ui.Text(c, tLabel).FontSize(12).SingleLine().
						Transition(selectionTransition).ContentTransition(textChange)
					if selected {
						txt.Bold().TextColor(ui.Hex("#ffffff"))
					} else {
						txt.TextColor(ui.RGBA(255, 255, 255, 0.70))
					}
				})
			})
		}
	})
}

func (a *App) cardGrid(c *ui.Context, key string, state *ui.GridState, scroll *ui.ScrollState, items []CatalogItem, extra float32, after func(CatalogItem)) ui.Element {
	return a.sharedCardGrid(c, key, state, scroll, items, extra, after)
}

func formatRelativeTime(ts int64) string {
	if ts <= 0 {
		return "刚刚"
	}
	now := time.Now().Unix()
	diff := now - ts
	if diff < 60 {
		return "刚刚"
	}
	if diff < 3600 {
		return fmt.Sprintf("%d分钟前", diff/60)
	}
	if diff < 86400 {
		return fmt.Sprintf("%d小时前", diff/3600)
	}
	if diff < 86400*30 {
		return fmt.Sprintf("%d天前", diff/86400)
	}
	return time.Unix(ts, 0).Format("01月02日")
}

func (a *App) catalogCardSized(c *ui.Context, item CatalogItem, width float32) {
	a.catalogCardContent(c, item, width)
}

func (a *App) fixedHeightCatalogCardSized(c *ui.Context, item CatalogItem, width, rowHeight float32) {
	a.catalogCardContent(c, item, width).Height(rowHeight)
}

func (a *App) catalogCardContent(c *ui.Context, item CatalogItem, width float32) ui.Element {
	t := c.Theme()
	card := ui.Column(c.Key(item.ID + "|" + item.EpisodeID)).Role(ui.RoleButton).Label(item.Title).
		Width(width).MinWidth(width).MaxWidth(width).Gap(6)
	if item.Kind == "source_novel" {
		card.Role(ui.RoleGroup).Label("原著信息")
	} else {
		card.Focusable()
	}
	shouldOpen := item.Kind != "source_novel" && (a.view != "reservations" || !item.Upcoming)
	if shouldOpen {
		hoverBackground(card, ui.Transparent, ui.RGBA(255, 255, 255, 0.04))
	}
	if shouldOpen && (card.Clicked() || card.Shortcut(0, ui.KeyEnter) || card.Shortcut(0, ui.KeySpace)) {
		go a.OpenItem(item)
	}

	topLeftBadge := ""
	topRightBadge := ""
	bottomRightBadge := ""
	if item.Kind == "source_novel" {
		topLeftBadge = "原著小说"
	}

	if a.view == "new" {
		if item.Completed {
			bottomRightBadge = "完结"
		}
	} else if a.view == "history" {
		if item.EpisodeCount > 0 {
			bottomRightBadge = historyEpisodeBadge(item)
		}
	} else if a.view == "favorites" {
		if item.EpisodeCount > 0 {
			topRightBadge = fmt.Sprintf("%d集", item.EpisodeCount)
		}
	} else if a.view == "likes" {
		if item.EpisodeCount > 0 {
			topRightBadge = fmt.Sprintf("%d集", item.EpisodeCount)
		}
	}

	posterLabel := fmt.Sprintf("poster:%s|%s", item.ID, item.EpisodeID)
	posterKey := c.Key(posterLabel)

	card.Children(func() {
		posterBox := ui.Box(posterKey).Size(width, width*4/3).Radius(8).Clip().Role(ui.RoleImage).Label(posterLabel)
		posterBox.Children(func() {
			a.image(c, item.CoverURL, width, width*4/3)
			if a.view == "history" {
				if item.CoverURL == "" {
					ui.Badge(c.Key("history-cover-pending"), "封面待补全").Absolute().Bottom(catalogOverlayInset).Left(catalogOverlayInset)
				} else if a.ArtworkFailed(item.CoverURL) {
					ui.Badge(c.Key("history-cover-failed"), "封面加载失败").Absolute().Bottom(catalogOverlayInset).Left(catalogOverlayInset)
				}
			}
			if topLeftBadge != "" {
				ui.Badge(c.Key("badge-tl"), topLeftBadge).Absolute().Top(catalogOverlayInset).Left(catalogOverlayInset).
					Padding(2, 6).Radius(4).Background(ui.Transparent).Material(artworkMaterial()).
					Bold().TextColor(ui.Hex("#ffffff"))
			}
			if bottomRightBadge != "" {
				ui.Row(c.Key("badge-br")).Absolute().Bottom(catalogOverlayInset).Right(catalogOverlayInset).
					Padding(2, 6).Radius(4).Material(artworkMaterial()).
					Children(func() {
						ui.Text(c, bottomRightBadge).FontSize(11).Bold().TextColor(ui.Hex("#ffffff"))
					})
			}
			if topRightBadge != "" {
				ui.Row(c.Key("badge-tr")).Absolute().Top(catalogOverlayInset).Right(catalogOverlayInset).
					Padding(2, 6).Radius(4).Material(artworkMaterial()).
					Children(func() {
						ui.Text(c, topRightBadge).FontSize(11).Bold().TextColor(ui.Hex("#ffffff"))
					})
			}
			if a.view == "favorites" || a.view == "likes" || a.view == "reservations" {
				label := map[string]string{"favorites": "取消收藏", "likes": "取消点赞", "reservations": "取消预约"}[a.view]
				overlay := ui.Row(c.Key("overlay:"+item.ID+"|"+label)).
					Absolute().Bottom(catalogOverlayInset).Left(catalogOverlayInset).Right(catalogOverlayInset).
					Height(catalogOverlayHeight).Padding(0, 12).Radius(catalogOverlayHeight/2).
					Shadow(0, 3, 8, 0, ui.RGBA(0, 0, 0, 0.22)).
					AlignItems(ui.Center).Justify(ui.Center).Role(ui.RoleButton).Label(label).
					Material(artworkMaterial())
				if overlay.Clicked() {
					libraryTab := libraryHistory
					switch a.view {
					case "favorites":
						libraryTab = libraryFavorites
					case "likes":
						libraryTab = libraryLikes
					case "reservations":
						libraryTab = libraryReservations
					}
					go a.RemoveLibraryItem(libraryTab, item)
				}
				overlay.Children(func() {
					ui.Text(c, label).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.92)).ContentTransition(textChange)
				})
			}
		})

		ui.Row(c.Key("title-row")).Width(width).Height(catalogTextLineHeight).AlignItems(ui.Center).Children(func() {
			title := ui.Text(c, item.Title).Bold().FontSize(13).TextColor(ui.Hex("#ffffff")).SingleLine().Grow(1).MinWidth(0).Tooltip(item.Title)
			if item.Kind != "source_novel" {
				title.Label("single-line-title")
			}
		})

		if a.view == "history" {
			ui.Row(c).Justify(ui.End).AlignItems(ui.Center).Width(width).Children(func() {
				ui.Text(c, formatRelativeTime(item.UpdatedAt)).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.50))
			})
		} else if a.view == "reservations" {
			if item.PublishAt != "" {
				ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Width(width).Children(func() {
					ui.Text(c, "上线日期").FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.60))
					ui.Text(c, formatPublishDate(item.PublishAt)).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.85))
				})
			}
		} else if a.view == "new" {
			if item.Upcoming {
				if item.PublishAt != "" {
					ui.Text(c, formatPublishDate(item.PublishAt)).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.60)).SingleLine().Width(width)
				}
				label := "预约"
				if item.Reserved {
					label = "取消预约"
				}
				if ui.Button(c, label).Height(catalogReservationButtonHeight).Padding(0, 12).Radius(14).Clicked() {
					go a.ToggleItemReservation(item)
				}
			} else {
				if item.Heat != "" {
					ui.Text(c, item.Heat).FontSize(12).Bold().TextColor(ui.Hex("#ff3358")).SingleLine().Width(width)
				}
			}
		} else if a.view != "favorites" && a.view != "likes" {
			if item.Upcoming && item.PublishAt != "" {
				ui.Text(c, formatPublishDate(item.PublishAt)).TextColor(t.TextMuted).SingleLine().Width(width)
			}
			if a.view != "search" && !item.Upcoming && item.BadgeText != "" {
				ui.Text(c, item.BadgeText).TextColor(t.TextMuted).Width(width)
			}
			if a.view != "search" && a.view != "detail" && !item.Upcoming && item.Category != "" {
				ui.Text(c, item.Category).TextColor(t.TextMuted).SingleLine().Width(width)
			}
			if !item.Upcoming && (item.Heat != "" || item.EpisodeCount > 0) {
				meta := item.Heat
				if item.EpisodeCount > 0 {
					meta = fmt.Sprintf("%d集", item.EpisodeCount)
					if item.Heat != "" {
						meta += " · " + item.Heat
					}
				}
				ui.Text(c, meta).TextColor(t.TextMuted).SingleLine().Width(width)
			}
			if item.Upcoming {
				label := "预约"
				if item.Reserved {
					label = "取消预约"
				}
				if ui.Button(c, label).Height(catalogReservationButtonHeight).Padding(0, 12).Radius(14).Clicked() {
					go a.ToggleItemReservation(item)
				}
			}
		}
	})
	return card
}

func (a *App) renderSearchBar(c *ui.Context) {
	query := a.query
	bar := ui.Row(c.Key("search-bar-wrap")).Height(38).Radius(19).
		Material(pageChromeMaterial()).
		Border(1, ui.RGBA(255, 255, 255, 0.08)).
		Padding(0, 14).AlignItems(ui.Center).Gap(10)
	bar.Children(func() {
		ui.Icon(c, searchIcon).Size(16, 16).TextColor(ui.RGBA(255, 255, 255, 0.50))
		ui.TextInputBase(c.Key("search-query"), &query).Search().Grow(1).
			Placeholder("搜索短剧、角色、演员...").
			Label("搜索短剧").
			OnTextKey(a.searchTextKey).
			OnChange(func() { a.SetQuery(query) }).
			OnSubmit(func() { a.SetQuery(query); go a.SearchSubmitted() })
		if query != "" {
			clearBtn := ui.Row(c.Key("search-clear-btn")).Size(20, 20).Radius(10).
				AlignItems(ui.Center).Justify(ui.Center).
				Role(ui.RoleButton).Label("清空搜索")
			hoverBackground(clearBtn, ui.RGBA(255, 255, 255, 0.12), ui.RGBA(255, 255, 255, 0.22))
			if clearBtn.Clicked() {
				a.query = ""
				a.generation++ // Discard an in-flight search response after clearing.
				a.suggestions = nil
				a.suggestionIndex = -1
				a.suggestionVersion++
				a.search = CatalogPage{}
			}
			clearBtn.Children(func() {
				ui.Icon(c, closeIcon).Size(12, 12).TextColor(ui.RGBA(255, 255, 255, 0.85))
			})
		}
	})
	if len(a.suggestions) > 0 {
		ui.Row(c.Key("search-suggestions")).Wrap().Gap(6).Padding(4, 0).Children(func() {
			for _, suggestion := range a.suggestions {
				s := suggestion
				chip := ui.Row(c.Key("sug:"+s)).Height(26).Padding(3, 12).Radius(13).
					Border(1, ui.RGBA(255, 255, 255, 0.09)).
					Role(ui.RoleButton).Label(s)
				hoverBackground(chip, ui.RGBA(255, 255, 255, 0.06), ui.RGBA(255, 255, 255, 0.12))
				if chip.Clicked() {
					a.query = s
					go a.SearchSubmitted()
				}
				chip.Children(func() {
					ui.Text(c, s).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.85))
				})
			}
		})
	}
}

func (a *App) renderSearch(c *ui.Context) {
	a.renderSearchBar(c)
	search := a.search
	search.Items = normalizeSearchCards(search.Items)
	if a.query == "" && len(search.Items) == 0 && !search.Loading && search.Error == "" {
		a.emptyView(c, "搜索短剧", "输入剧名、角色或演员，发现想看的短剧")
		return
	}
	if search.Loading && len(search.Items) == 0 {
		a.gridSkeleton(c, 12, a.gridContentWidth(c))
		return
	}
	if search.Error != "" {
		if len(search.Items) == 0 {
			a.errorView(c, search.Error, func() { go a.SearchSubmitted() })
			return
		}
		inlineError(c, search.Error, func() { go a.SearchSubmitted() })
	}
	if len(search.Items) == 0 {
		a.emptyView(c, "暂无搜索结果", "请尝试其他关键词")
		return
	}
	if a.searchGrid == nil {
		a.searchGrid = &ui.GridState{}
	}
	a.cardGrid(c, "search-grid", a.searchGrid, nil, search.Items, 0, nil).Grow(1)
}

func (a *App) renderLibrary(c *ui.Context, tab LibraryTab) {
	state := a.library
	library := state.History
	grid := 0
	switch tab {
	case libraryFavorites:
		library, grid = state.Favorites, 1
	case libraryLikes:
		library, grid = state.Likes, 2
	case libraryReservations:
		library, grid = state.Reserved, 3
	}

	if tab == libraryHistory {
		ui.Row(c.Key("history-header")).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				a.renderSlidingTabs(c, "hist-tabs", "history-filter", []slidingTabItem{
					{"", "全部"},
					{"unwatched", "未看完"},
					{"completed", "已看完"},
				}, a.historyFilter, func(key string) {
					a.historyFilter = key
					a.refreshVisibleLibraryLocked(libraryHistory)
					a.invalidate()
				})
			})
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				query := a.historyQuery
				sWrap := ui.Row(c.Key("hist-search")).Height(32).Width(200).Radius(16).
					Material(pageChromeMaterial()).
					Border(1, ui.RGBA(255, 255, 255, 0.08)).
					Padding(0, 10).AlignItems(ui.Center).Gap(6)
				sWrap.Children(func() {
					ui.Icon(c, searchIcon).Size(14, 14).TextColor(ui.RGBA(255, 255, 255, 0.50))
					ui.TextInputBase(c.Key("hist-search-input"), &query).Search().Grow(1).
						Placeholder("搜索历史剧名...").Label("搜索历史").
						OnChange(func() { a.SetHistoryQuery(query) })
					if query != "" {
						histClear := ui.Row(c.Key("hist-search-clear")).Size(16, 16).Radius(8).
							Background(ui.RGBA(255, 255, 255, 0.12)).
							AlignItems(ui.Center).Justify(ui.Center).
							Role(ui.RoleButton).Label("清空搜索")
						if histClear.Clicked() {
							a.SetHistoryQuery("")
						}
						histClear.Children(func() {
							ui.Icon(c, closeIcon).Size(10, 10).TextColor(ui.RGBA(255, 255, 255, 0.85))
						})
					}
				})
			})
		})
	}
	if tab == libraryFavorites || tab == libraryLikes {
		favorites, likes := a.libraryAt(libraryFavorites), a.libraryAt(libraryLikes)
		favCount := libraryCountLabel(*favorites)
		likeCount := libraryCountLabel(*likes)
		ui.Row(c.Key("fav-header")).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Padding(3, 10).Radius(12).Background(ui.RGBA(255, 255, 255, 0.06)).Children(func() {
					ui.Textf(c, "共 %s 部收藏 · %s 个点赞", favCount, likeCount).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.65))
				})
			})
			a.renderSlidingTabs(c, "fav-tabs", "fav-views", []slidingTabItem{
				{"favorites", fmt.Sprintf("收藏 %s", favCount)},
				{"likes", fmt.Sprintf("点赞 %s", likeCount)},
			}, a.view, func(vId string) {
				go a.OpenView(vId)
			})
		})
	}
	if tab == libraryReservations {
		reservations := library.Items
		total := libraryCountLabel(library)
		onlineCount, pendingCount := total, total
		if library.Loaded || len(reservations) > 0 || library.Empty {
			online, pending := reservationCounts(reservations)
			onlineCount, pendingCount = fmt.Sprint(online), fmt.Sprint(pending)
		}
		ui.Row(c.Key("res-header")).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Padding(3, 10).Radius(12).Background(ui.RGBA(255, 255, 255, 0.06)).Children(func() {
					ui.Textf(c, "新剧预约 · 共 %s 部 · 已上线 %s · 待上线 %s", total, onlineCount, pendingCount).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.65))
				})
			})
			resSelected := "online"
			if a.reservationsPending {
				resSelected = "pending"
			}
			a.renderSlidingTabs(c, "res-tabs", "res-pending", []slidingTabItem{
				{"online", fmt.Sprintf("已上线 %s", onlineCount)},
				{"pending", fmt.Sprintf("待上线 %s", pendingCount)},
			}, resSelected, func(pKey string) {
				a.reservationsPending = (pKey == "pending")
				a.refreshVisibleLibraryLocked(libraryReservations)
			})
		})
	}
	if library.Loading && len(library.Items) == 0 {
		a.gridSkeleton(c, 12, a.gridContentWidth(c))
		return
	}

	if library.Error != "" && !(tab == libraryHistory && len(library.Items) > 0) {
		if len(library.Items) == 0 {
			a.errorView(c, library.Error, func() { go a.RefreshLibrary(tab) })
			return
		}
		ui.Row(c.Key("library-sync-warning")).Justify(ui.SpaceBetween).AlignItems(ui.Center).
			Padding(8, 12).Radius(8).Background(ui.RGBA(230, 80, 80, 0.10)).Children(func() {
			ui.Text(c, library.Error).TextColor(c.Theme().Danger)
			if ui.PrimaryButton(c, "立即重试").Clicked() {
				go a.RefreshLibrary(tab)
			}
		})
	}
	if library.Empty {
		a.emptyView(c, "暂无记录", "浏览和播放后才会形成记录")
		return
	}
	visibleItems := a.visibleLibrary(tab)
	if len(visibleItems) == 0 {
		a.emptyView(c, "暂无匹配记录", "可以调整筛选条件")
		return
	}
	if a.library.Grids[grid] == nil {
		a.library.Grids[grid] = &ui.GridState{}
	}
	a.library.Grids[grid].Key = func(item int) any { return visibleItems[item].ID + "|" + visibleItems[item].EpisodeID }
	a.library.Grids[grid].Label = func(item int) string {
		title := visibleItems[item].Title
		cut := len(title)
		runes := 0
		for index := range title {
			if runes == 18 {
				cut = index
				break
			}
			runes++
		}
		if cut == len(title) {
			return title
		}
		return title[:cut] + "…"
	}
	a.cardGrid(c, "library-grid", a.library.Grids[grid], nil, visibleItems, 0, nil).Grow(1)
}

func reservationCounts(items []CatalogItem) (int, int) {
	online, pending := 0, 0
	for _, item := range items {
		if item.Upcoming {
			pending++
		} else {
			online++
		}
	}
	return online, pending
}

func (a *App) gridSkeleton(c *ui.Context, count int, width float32) {
	dramaSkeleton(c, count, width)
}

func (a *App) filterSkeleton(c *ui.Context) {
	skeleton := pagePlaceholder(c)
	ui.Column(c.Key("filter-skeleton")).Padding(16, 20).Gap(14).Radius(12).Border(1, c.Theme().Border).Background(c.Theme().Surface).Children(func() {
		a.filterSkeletonRow(c.Key("genre"), skeleton, 3)
		a.filterSkeletonRow(c.Key("topic"), skeleton, 5)
		a.filterSkeletonRow(c.Key("status"), skeleton, 4)
	})
}

func (a *App) filterSkeletonRow(c *ui.Context, skeleton placeholder, chips int) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		skeleton.text(c.Key("label"), 12, 1.3).Width(40)
		ui.Row(c.Key("chips")).Gap(8).Children(func() {
			for index := range chips {
				skeleton.bone(c.Key(index), 14).Size(52, 28)
			}
		})
	})
}

func (a *App) rankSkeleton(c *ui.Context) {
	skeleton := pagePlaceholder(c)
	ui.Column(c.Key("rank-skeleton")).Grow(1).Gap(10).Children(func() {
		for index := range 8 {
			ui.Row(c.Key(index)).Gap(16).AlignItems(ui.Center).Padding(12, 18).Radius(12).Border(1, c.Theme().Border).Background(c.Theme().Surface).Children(func() {
				skeleton.bone(c.Key("position"), 6).Size(32, 26)
				skeleton.bone(c.Key("poster"), 8).Size(50, 68)
				ui.Column(c.Key("copy")).Grow(1).Gap(8).Children(func() {
					skeleton.text(c.Key("title"), 14, 1.3).WidthPercent(80)
					skeleton.text(c.Key("metadata"), 12, 1.3).Width(130)
				})
				skeleton.bone(c.Key("action"), 14).Size(82, 28)
			})
		}
	})
}

func (a *App) emptyView(c *ui.Context, title, description string) {
	ui.Column(c.Key("empty-view:" + title)).MarginEnd(12).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Text(c, title).Bold()
		ui.Text(c, description).TextColor(c.Theme().TextMuted)
	})
}

func (a *App) errorView(c *ui.Context, message string, retry func()) {
	ui.Column(c.Key("error-view:" + message)).MarginEnd(12).Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, message).TextColor(c.Theme().Danger)
		if retry != nil && ui.PrimaryButton(c, "重试").Clicked() {
			go retry()
		}
	})
}

func (a *App) image(c *ui.Context, source string, width, height float32) {
	// Always retain the same sized host, whether loading, failed or decoded.
	// Bounds are from the previous layout, so wait one frame for new hosts.
	host := ui.Box(c).Size(width, height).Radius(10).Clip().Background(c.Theme().SurfaceHover)
	bounds := host.Bounds()
	if bounds.W <= 0 || bounds.H <= 0 {
		c.Invalidate()
		return
	}
	windowWidth, windowHeight := c.Size()
	const preload = float32(300)
	if bounds.Y+bounds.H < -preload || bounds.Y > windowHeight+preload ||
		bounds.X+bounds.W < 0 || bounds.X > windowWidth {
		return
	}
	bitmap := a.Artwork(source)
	loading := a.artwork != nil && a.artwork.isLoading(source)
	if loading {
		skeleton := pagePlaceholder(c)
		host.Draw(func(p *ui.Painter, r ui.Rect) { skeleton.paint(p, r, 10) })
	}
	host.Children(func() {
		pose := ui.Pose{}
		if bitmap == nil {
			pose.Fade = 1
		}
		ui.Image(c.Key("artwork-image"), bitmap).Size(width, height).Fit(ui.Cover).Radius(10).
			AnimatePose(pose, artworkFade).ContentTransition(ui.ContentTransition{Animation: &artworkFade})
	})
}

func historyEpisodeBadge(item CatalogItem) string {
	if item.Completed {
		return "已看完"
	}
	if item.ResumeEpisode > 0 && item.EpisodeCount > 0 {
		return fmt.Sprintf("看到第%d集", min(item.ResumeEpisode, item.EpisodeCount))
	}
	if item.EpisodeCount > 0 {
		return fmt.Sprintf("共%d集", item.EpisodeCount)
	}
	return ""
}

func parsePublishDate(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02", "20060102"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, true
		}
	}
	return time.Time{}, false
}

func formatPublishDate(value string) string {
	if date, ok := parsePublishDate(value); ok {
		return date.Format("2006-01-02")
	}
	return value
}
