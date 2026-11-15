package uiapp

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func (a *App) renderRank(c *ui.Context) {
	rank := a.rank

	// Top Category Switcher Track: [全部] [系列剧] [漫剧] [真人剧] [AI剧] [演员]
	if len(rank.Contents) > 0 {
		var tabs []slidingTabItem
		for _, content := range rank.Contents {
			tabs = append(tabs, slidingTabItem{Key: content.ID, Label: content.Name})
		}
		a.renderSlidingTabs(c, "rank-contents-track", "rank-contents", tabs, rank.Content, func(cID string) {
			for _, content := range rank.Contents {
				if content.ID == cID {
					board := ""
					if len(content.Boards) > 0 {
						board = content.Boards[0].ID
					}
					go a.SelectRank(cID, board)
					break
				}
			}
		})
	}

	// Sub-category Pills (Board Tabs)
	for _, content := range rank.Contents {
		if content.ID != rank.Content {
			continue
		}
		if len(content.Boards) > 0 {
			ui.Row(c.Key("rank-boards-row")).Wrap().Gap(8).Padding(4, 0).Children(func() {
				for _, board := range content.Boards {
					bID := board.ID
					bName := board.Name
					selected := bID == rank.Board
					a.pillChip(c, "board:"+bID, bName, selected, func() {
						go a.SelectRank(content.ID, bID)
					})
				}
			})
		}
	}

	if rank.Error != "" {
		if len(rank.Items) == 0 {
			a.errorView(c, rank.Error, func() { go a.LoadRank(false) })
			return
		}
		inlineError(c, rank.Error, func() { go a.LoadRank(rank.Cursor != "") })
	}
	if rank.Loading && len(rank.Items) == 0 {
		a.rankSkeleton(c)
		return
	}
	if len(rank.Items) == 0 {
		a.emptyView(c, "暂无上榜短剧", "可以选择其他榜单")
		return
	}
	if a.rank.List == nil {
		a.rank.List = &ui.ListState{}
	}
	a.rank.List.NearEnd = 3
	a.rank.List.EndReached = func(int) {
		if rank.HasMore && !rank.Loading && rank.Error == "" {
			go a.LoadRank(true)
		}
	}
	a.rank.List.Key = func(row int) any {
		item := rank.Items[row]
		return fmt.Sprintf("%s|%s|%d", item.SeriesID, item.UserID, row)
	}
	a.rank.List.Label = func(row int) string { return rank.Items[row].Title }
	ui.List(c.Key(fmt.Sprintf("rank-result:%d", a.rankResultVersion)), a.rank.List, len(rank.Items), func(row int) {
		item := rank.Items[row]
		a.rankItem(c, row, item)
	}).Grow(1).TrackScroll(&a.rankScroll).Scrollbars(false).Transition(resultTransition)
}

func (a *App) rankItem(c *ui.Context, index int, item ipc.RankItemDTO) {
	actorID := item.UserID
	if actorID == "" {
		actorID = item.ActorID
	}
	id, cover := item.SeriesID, item.CoverURL
	if item.ItemType == "actor" {
		id = actorID
		if item.AvatarURL != "" {
			cover = item.AvatarURL
		}
	}
	positionLabel := fmt.Sprintf("第%d名", item.Position)
	if item.Promoted {
		positionLabel = "平台推荐"
	}

	row := ui.Row(c.Key(fmt.Sprintf("%s|%s|%d", item.SeriesID, actorID, index))).
		Gap(16).AlignItems(ui.Center).Padding(12, 18).Radius(12).
		Border(1, ui.RGBA(255, 255, 255, 0.06)).
		Background(ui.RGBA(255, 255, 255, 0.035)).
		Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.25))

	hoverBackground(row, ui.RGBA(255, 255, 255, 0.035), ui.RGBA(255, 255, 255, 0.065))
	row.Border(1, ui.RGBA(255, 255, 255, 0.06))
	row.Role(ui.RoleButton).Label(positionLabel + " " + item.Title).Focusable()
	if item.ItemType != "actor" && (row.Clicked() || row.Shortcut(0, ui.KeyEnter) || row.Shortcut(0, ui.KeySpace)) {
		go a.OpenItem(CatalogItem{ID: id, ResumeEpisode: 1, ResumePosition: -1})
	}

	row.Children(func() {
		// Big rank number or promoted text
		ui.Box(c).Width(36).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if item.Promoted {
				ui.Text(c, "平台推荐").FontSize(11).Bold().TextColor(ui.Hex("#fe2c55"))
			} else {
				posStr := fmt.Sprintf("%d", item.Position)
				color := rankPositionColor(item.Position, false)
				size := float32(22)
				if item.Position >= 10 {
					size = 18
				}
				ui.Text(c, posStr).FontSize(size).Bold().Italic().TextColor(color)
			}
		})

		// Poster thumbnail (3:4)
		a.image(c, cover, 48, 64)

		// Title & metadata
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, item.Title).FontSize(14).Bold().TextColor(ui.Hex("#ffffff")).SingleLine()
			meta := []string{}
			if item.ItemType != "actor" {
				meta = append(meta, "短剧")
			}
			if item.Heat != "" {
				meta = append(meta, item.Heat)
			}
			if item.EpisodeCount > 0 {
				meta = append(meta, fmt.Sprintf("%d集", item.EpisodeCount))
			}
			if item.ItemType == "actor" {
				if item.Intro != "" {
					meta = append(meta, item.Intro)
				}
				if len(item.Details) > 0 {
					meta = append(meta, strings.Join(item.Details, " · "))
				}
			}
			if len(meta) > 0 {
				ui.Text(c, strings.Join(meta, " · ")).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.60)).SingleLine()
			}
		})

		// Right Action Button
		if item.ItemType == "actor" {
			actBtn := ui.Row(c.Key("actor-btn:"+actorID)).Height(32).Padding(0, 14).Radius(8).
				AlignItems(ui.Center).Justify(ui.Center).Role(ui.RoleButton).Label("主页")
			hoverBackground(actBtn, ui.RGBA(255, 255, 255, 0.08), ui.RGBA(255, 255, 255, 0.14))
			actBtn.Border(1, ui.RGBA(255, 255, 255, 0.12))
			if actBtn.Clicked() {
				go a.OpenActor(actorID)
			}
			actBtn.Children(func() {
				ui.Text(c, "主页").FontSize(12).TextColor(ui.Hex("#ffffff"))
			})
		} else {
			playBtn := ui.Row(c.Key("play-btn:"+id)).Height(32).Padding(0, 16).Radius(8).
				Background(ui.RGBA(209, 27, 66, 1)).Border(1, ui.RGBA(255, 255, 255, 0.20)).
				Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.35)).
				AlignItems(ui.Center).Justify(ui.Center).Role(ui.RoleButton).Label("立即播放")
			hoverBackground(playBtn, ui.RGB(209, 27, 66), ui.RGB(229, 36, 75))
			if playBtn.Clicked() {
				go a.OpenItem(CatalogItem{ID: id, ResumeEpisode: 1, ResumePosition: -1})
			}
			playBtn.Children(func() {
				ui.Text(c, "立即播放").FontSize(12).Bold().TextColor(ui.Hex("#ffffff"))
			})
		}
	})
}

func rankPositionColor(position int, promoted bool) ui.Color {
	if promoted {
		return ui.RGB(255, 44, 85)
	}
	switch position {
	case 1:
		return ui.RGB(255, 215, 0)
	case 2:
		return ui.RGB(209, 213, 219)
	case 3:
		return ui.RGB(245, 158, 11)
	}
	return ui.RGBA(255, 255, 255, 0.45)
}

func (a *App) renderReleases(c *ui.Context) {
	value := a.releases
	a.renderSlidingTabs(c, "releases-track", "releases-kind", []slidingTabItem{
		{"hot", "热播新剧"},
		{"calendar", "新剧日历"},
	}, func() string {
		if value.Calendar {
			return "calendar"
		}
		return "hot"
	}(), func(key string) {
		go a.SelectRelease(key == "calendar", "")
	})
	if value.Calendar {
		ui.Row(c).Wrap().Gap(8).Padding(4, 0).Children(func() {
			for _, date := range value.Dates {
				dStr := date
				selected := dStr == value.Date
				tab := ui.Column(c.Key(dStr)).Padding(6, 14).Radius(10).
					Border(1, ui.RGBA(255, 255, 255, 0.08)).
					Background(ui.RGBA(255, 255, 255, 0.04)).AlignItems(ui.Center).Gap(2)
				if selected {
					tab.LinearGradient(ui.LinearGradient{
						From:  ui.Hex("#d91b42"),
						To:    ui.Hex("#b81235"),
						Angle: 135,
					}).Border(1, ui.RGBA(255, 255, 255, 0.22)).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.35))
				}
				hoverBackground(tab, ui.RGBA(255, 255, 255, 0.04), ui.RGBA(255, 255, 255, 0.08))
				tab.Border(1, ui.RGBA(255, 255, 255, 0.08))
				tab.Role(ui.RoleButton).Label(formatCalendarLabel(dStr)).Focusable()
				if tab.Clicked() || tab.Shortcut(0, ui.KeyEnter) || tab.Shortcut(0, ui.KeySpace) {
					go a.SelectRelease(true, dStr)
				}
				tab.Children(func() {
					txtW := ui.Text(c, calendarWeekday(dStr)).FontSize(11)
					txtD := ui.Text(c, calendarDate(dStr)).FontSize(13).Bold()
					if selected {
						txtW.TextColor(ui.Hex("#ffffff"))
						txtD.TextColor(ui.Hex("#ffffff"))
					} else {
						txtW.TextColor(ui.RGBA(255, 255, 255, 0.60))
						txtD.TextColor(ui.RGBA(255, 255, 255, 0.85))
					}
				})
			}
		})
	}
	if value.Error != "" {
		if len(value.Items) == 0 {
			a.errorView(c, value.Error, func() { go a.LoadReleases() })
			return
		}
		inlineError(c, value.Error, func() { go a.LoadReleases() })
	}
	if value.Loading && len(value.Items) == 0 {
		a.releaseSkeleton(c, value)
		return
	}
	if len(value.Items) == 0 {
		a.emptyView(c, "当前新剧热榜暂无剧集", "可以选择其他日期")
		return
	}
	if len(value.Items) <= 3 {
		a.releaseCardGrid(c, value.Items)
		return
	}
	if a.releaseGrid == nil {
		a.releaseGrid = &ui.GridState{}
	}
	a.cardGrid(c, "releases-grid", a.releaseGrid, nil, value.Items, 0, nil).Grow(1)
}

func (a *App) releaseCardGrid(c *ui.Context, items []CatalogItem) {
	_, windowHeight := c.Size()
	posterWidth := newReleasePosterWidth(float32(windowHeight))
	ui.Column(c.Key("releases-sparse-content")).FillWidth().Children(func() {
		ui.Column(c.Key(fmt.Sprintf("release-result:%d", a.releaseResultVersion))).FillWidth().Gap(18).Transition(resultTransition).Children(func() {
			for _, item := range items {
				a.releaseFeatureRow(c, item, posterWidth)
			}
		})
	})
}

func (a *App) releaseSkeleton(c *ui.Context, value ReleaseState) {
	if value.Calendar {
		_, windowHeight := c.Size()
		posterWidth := newReleasePosterWidth(float32(windowHeight))
		posterHeight := posterWidth * posterAspectRatio
		skeleton := pagePlaceholder(c)
		ui.Column(c.Key("releases-sparse-content")).FillWidth().Children(func() {
			ui.Column(c.Key("release-skeleton")).FillWidth().Gap(18).Children(func() {
				for range min(3, max(1, len(value.Dates))) {
					ui.Row(c).FillWidth().Gap(18).AlignItems(ui.Stretch).MinHeight(posterHeight).Children(func() {
						skeleton.bone(c.Key("poster"), 10).Width(posterWidth).Height(posterHeight).
							Role(ui.RoleImage).Label("加载海报占位")
						ui.Column(c).Grow(1).FillHeight().MinWidth(0).Padding(18, 20).Gap(12).
							Background(ui.RGBA(20, 21, 28, 255)).
							Border(1, ui.RGBA(255, 255, 255, 0.08)).Radius(12).Children(func() {
							skeleton.text(c.Key("title"), 22, 1.2).WidthPercent(52)
							skeleton.text(c.Key("date"), 13, 1.3).WidthPercent(28)
							skeleton.text(c.Key("category"), 13, 1.3).WidthPercent(20)
							skeleton.bone(c.Key("action"), 9).Size(92, 38)
						})
					})
				}
			})
		})
		return
	}
	posterGridSkeleton(c, 12, a.gridContentWidth(c), nil)
}

func (a *App) releaseFeatureRow(c *ui.Context, item CatalogItem, posterWidth float32) {
	posterHeight := posterWidth * posterAspectRatio
	group := ui.Column(c.Key("release-feature-group:" + item.ID + "|" + item.EpisodeID)).FillWidth()
	group.Role(ui.RoleGroup).Label("release-feature-bounds:" + item.ID + "|" + item.EpisodeID)
	group.Children(func() {
		ui.Row(c.Key("release-feature:" + item.ID + "|" + item.EpisodeID)).FillWidth().Gap(18).
			AlignItems(ui.Stretch).MinHeight(posterHeight).Children(func() {
			posterBox := ui.Box(c.Key("release-feature-poster:" + item.ID + "|" + item.EpisodeID)).
				Width(posterWidth).Height(posterHeight).Radius(10).Clip().Role(ui.RoleImage).
				Label(posterLabel(item))
			posterBox.Children(func() {
				a.image(c, item.CoverURL, posterWidth, posterHeight)
			})

			panel := ui.Column(c.Key("release-feature-panel:"+item.ID+"|"+item.EpisodeID)).
				Grow(1).FillHeight().MinWidth(0).Padding(18, 20).Gap(12).
				Background(ui.RGBA(20, 21, 28, 255)).
				Border(1, ui.RGBA(255, 255, 255, 0.08)).Radius(12)
			panel.Role(ui.RoleGroup).Label("release-feature-panel:" + item.ID + "|" + item.EpisodeID)
			panel.Children(func() {
				ui.Text(c, item.Title).FontSize(22).Bold().TextColor(ui.Hex("#ffffff")).WidthPercent(100)
				if item.PublishAt != "" {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "上线日期").FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.58))
						ui.Text(c, formatPublishDate(item.PublishAt)).FontSize(13).Bold().
							TextColor(ui.RGBA(255, 255, 255, 0.88))
					})
				}
				if item.Category != "" {
					ui.Text(c, item.Category).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.70))
				}
				if item.EpisodeCount > 0 {
					ui.Textf(c, "共 %d 集", item.EpisodeCount).FontSize(13).
						TextColor(ui.RGBA(255, 255, 255, 0.70))
				}
				_, windowHeight := c.Size()
				if item.Description != "" && windowHeight >= 700 {
					ui.Text(c, item.Description).FontSize(13).
						TextColor(ui.RGBA(255, 255, 255, 0.78)).WidthPercent(100)
				}

				reservation := ui.Row(c.Key("release-feature-reservation:"+item.ID+"|"+item.EpisodeID)).
					Height(38).Padding(0, 18).Radius(9).AlignItems(ui.Center).
					Role(ui.RoleButton).Label(reservationLabel(item)).Focusable()
				if item.Reserved {
					reservation.Background(ui.RGBA(255, 255, 255, 0.07)).
						Border(1, ui.RGBA(255, 255, 255, 0.16))
				} else {
					reservation.LinearGradient(ui.LinearGradient{
						From:  ui.Hex("#d91b42"),
						To:    ui.Hex("#b81235"),
						Angle: 135,
					}).Border(1, ui.RGBA(255, 255, 255, 0.22))
				}
				hoverBackground(reservation, ui.RGBA(255, 255, 255, 0.10), ui.RGBA(255, 255, 255, 0.18))
				if reservation.Clicked() || reservation.Shortcut(0, ui.KeyEnter) || reservation.Shortcut(0, ui.KeySpace) {
					go a.ToggleItemReservation(item)
				}
				reservation.Children(func() {
					ui.Text(c, reservationLabel(item)).FontSize(13).Bold().TextColor(ui.Hex("#ffffff"))
				})
			})
		})
	})
}

func reservationLabel(item CatalogItem) string {
	if item.Reserved {
		return "取消预约"
	}
	return "预约"
}

func posterLabel(item CatalogItem) string {
	return fmt.Sprintf("poster:%s|%s", item.ID, item.EpisodeID)
}
func (a *App) renderActor(c *ui.Context) {
	actor := a.actor
	sectionHeader(c, "作品")

	if actor.Profile != nil {
		profile := actor.Profile
		ui.Row(c).Gap(12).Children(func() {
			a.image(c, profile.AvatarURL, 90, 90)
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, profile.Name).FontSize(22).Bold()
				ui.Text(c, profile.Description)
				ui.Text(c, strings.Join(append(append([]string(nil), profile.Tags...), profile.Labels...), " · "))
				ui.Textf(c, "%d 粉丝 · %d 获赞 · %d 部参演短剧", profile.Followers, profile.Likes, len(actor.Works))
			})
		})
	}
	if actor.Error != "" {
		a.errorView(c, actor.Error, func() { go a.loadActor(actor.Profile != nil) })
	}
	if actor.Loading && actor.Profile == nil {
		ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Size(90, 90).Radius(45).Background(c.Theme().SurfaceHover)
			ui.Column(c).Grow(1).Gap(12).Children(func() {
				ui.Box(c).Width(180).Height(26).Radius(8).Background(c.Theme().SurfaceHover)
				ui.Row(c).Gap(18).Children(func() {
					for range 3 {
						ui.Box(c).Size(64, 22).Radius(8).Background(c.Theme().SurfaceHover)
					}
				})
				ui.Box(c).Height(13).Radius(6).Background(c.Theme().SurfaceHover).FillWidth()
			})
		})
		ui.Box(c).Height(18)
		a.gridSkeleton(c, 12, a.gridContentWidth(c))
		return
	}
	if a.actorWorksList == nil {
		a.actorWorksList = &ui.ListState{}
	}
	width := a.gridContentWidth(c)
	columns := dramaColumns(width)
	rows := (len(actor.Works) + columns - 1) / columns
	state := a.actorWorksList
	state.Key = func(row int) any {
		if row == rows {
			return "actor-footer"
		}
		item := actor.Works[row*columns]
		return item.ID + "|" + item.EpisodeID
	}
	state.NearEnd = 2
	state.EndReached = func(int) {
		if actor.HasMore && !actor.Loading && actor.Error == "" {
			go a.loadActor(true)
		}
	}
	ui.List(c.Key("actor-grid"), state, rows+1, func(row int) {
		if row == rows {

			a.continuePageInView(c, "actor-page-end", pageFillVersion{actor.ID, actor.Cursor, len(actor.Works)}, 0,
				a.actorScroll.MaxY <= 0 && actor.HasMore && !actor.Loading && actor.Error == "", func() { a.loadActor(true) })

			return
		}
		first := row * columns
		a.posterRow(c, actor.Works[first:min(first+columns, len(actor.Works))], width)
	}).Grow(1).TrackScroll(&a.actorScroll).Scrollbars(false)

}
func (a *App) renderCollection(c *ui.Context, label string, value DetailCollection, retry func()) {
	if value.Title != "" {
		label = value.Title
	}
	if !value.Loading && value.Error == "" && len(value.Items) == 0 {
		return
	}
	section := ui.Column(c.Key("detail-collection:" + label)).Gap(8)
	section.Children(func() {
		ui.Text(c, label).Bold().FontSize(15).TextColor(ui.Hex("#ffffff"))
		if value.Loading && len(value.Items) == 0 {
			return
		}
		if value.Error != "" {
			inlineError(c, value.Error, retry)
			if len(value.Items) == 0 {
				return
			}
		}
		ui.ScrollHorizontal(c.Key("col-scroll-" + label)).Scrollbars(false).Height(catalogRowHeight(value.Items)).Children(func() {
			ui.Row(c.Key("collection-" + label)).Gap(c.Theme().Space(3)).Children(func() {
				for _, item := range value.Items {
					a.catalogCard(c, item)
				}
			})
		})
	})
}

func calendarWeekday(value string) string {
	date, ok := parseCalendarDate(value)
	if !ok {
		return "日期"
	}
	return []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[date.Weekday()]
}

func calendarDate(value string) string {
	date, ok := parseCalendarDate(value)
	if !ok {
		return value
	}
	return fmt.Sprintf("%02d/%02d", int(date.Month()), date.Day())
}

func formatCalendarLabel(value string) string {
	date, ok := parseCalendarDate(value)
	if !ok {
		return value
	}
	return date.Format("2006-01-02")
}

func parseCalendarDate(value string) (time.Time, bool) {
	for _, layout := range []string{"20060102", "2006-01-02"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, true
		}
	}
	return time.Time{}, false
}
