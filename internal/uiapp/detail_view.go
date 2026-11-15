package uiapp

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

const (
	detailEpisodeGap      float32 = 10
	detailEpisodeHeight   float32 = 46
	detailTabsRow                 = 1
	detailFirstEpisodeRow         = 3
)

var detailPlayIcon = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z" fill="currentColor"/></svg>`))

type detailRowKey struct {
	Series, Kind string
	Index        int
}

// List supplies virtualization and pinned section headers; Tabs supplies the
// native keyboard/focus behavior and accent underline (MyGo v0.9.0). One list
// scrolls the introduction and episodes, without nested vertical scrollers.
func (a *App) renderDetail(c *ui.Context) {
	d := a.detail
	if d.Loading && d.Title == "" {
		a.detailSkeleton(c)
		return
	}
	if d.Error != "" && d.Title == "" {
		a.errorView(c, d.Error, func() { go a.OpenDetail(d.ID) })
		return
	}
	host := ui.Column(c.Key("detail-content")).Fill().MinWidth(0).MinHeight(0)
	width := host.Bounds().W
	if width <= 0 {
		width = a.gridContentWidth(c)
		c.Invalidate()
	}
	columns, cellWidth := detailEpisodeGeometry(width, c.Preferences().TextScale)
	tab := a.detailTab
	sections := detailRelatedSections(d)
	rows := detailFirstEpisodeRow + (len(d.Episodes)+columns-1)/columns
	if tab == detailTabRelated {
		rows = 2 + max(1, len(sections))
	}
	if tab == detailTabEpisodes && len(d.Episodes) == 0 {
		rows = 4
	}
	if tab == detailTabReviews {
		rows = 4 + len(d.Reviews.Items)
	}
	if a.detailEpisodesList == nil {
		a.detailEpisodesList = &ui.ListState{}
	}
	state := a.detailEpisodesList
	state.NearEnd = 2
	state.EndReached = func(int) {
		if tab == detailTabReviews && d.Reviews.Loaded && d.Reviews.HasMore && !d.Reviews.Loading && !d.Reviews.LoadingMore && d.Reviews.Error == "" {
			go a.LoadDetailReviews(true)
		}
	}
	state.Header = func(row int) bool { return row == detailTabsRow }
	state.Key = func(row int) any {
		kind, index := "hero", 0
		switch {
		case row == 0:
		case row == 1:
			kind = "tabs"
		case tab == detailTabRelated:
			kind = "empty-related"
			if row >= 2 && row-2 < len(sections) {
				kind = sections[row-2]
			}
		case tab == detailTabReviews:
			kind = "review-footer"
			if row == 2 {
				kind = "review-summary"
			} else if row >= 3 && row < 3+len(d.Reviews.Items) {
				return struct{ Series, Review string }{d.ID, d.Reviews.Items[row-3].ID}
			}
		case row == 2:
			kind = "episode-tools"
		case row >= 3:
			kind = "episodes"
			index = (row - 3) * columns
		}
		return detailRowKey{d.ID, kind, index}
	}
	state.Label = func(row int) string {
		if row == 0 {
			return "剧集介绍"
		}
		if row == 1 {
			return "剧集内容"
		}
		if tab == detailTabEpisodes && row >= 3 && len(d.Episodes) > 0 {
			return fmt.Sprintf("第%d集至第%d集", (row-3)*columns+1, min((row-2)*columns, len(d.Episodes)))
		}
		return ""
	}
	host.Children(func() {
		ui.List(c.Key("detail-page-list"), state, rows, func(row int) {
			switch {
			case row == 0:
				a.renderDetailHero(c, d, width)
			case row == 1:
				a.renderDetailTabs(c, d)
			case tab == detailTabRelated:
				if len(sections) == 0 {
					a.emptyView(c, "暂无相关作品", "")
					return
				}
				a.renderDetailRelated(c, d, sections[row-2], width)
			case tab == detailTabReviews:
				if row == 2 {
					a.renderDetailReviewSummary(c, d)
				} else if row >= 3 && row < 3+len(d.Reviews.Items) {
					a.renderDetailReview(c, d.Reviews.Items[row-3])
				} else {
					a.renderDetailReviewFooter(c, d)
				}
			case row == 2:
				a.renderDetailEpisodeTools(c, d)
			case len(d.Episodes) == 0:
				label := "暂无可播放选集"
				if d.Upcoming {
					label = "剧集尚未上线"
				}
				ui.Text(c, label).TextColor(c.Theme().TextMuted).Padding(20, 0)
			default:
				a.renderDetailEpisodeRow(c, d.Episodes, (row-3)*columns, columns, cellWidth)
			}
		}).Fill().Scrollbars(false).TrackScroll(&a.detailScroll).Label("剧集详情列表")
	})
}

func detailEpisodeGeometry(width, textScale float32) (int, float32) {
	minimum := float32(126) * max(1, textScale)
	columns := max(1, int((width+detailEpisodeGap)/(minimum+detailEpisodeGap)))
	return columns, max(1, (width-float32(columns-1)*detailEpisodeGap)/float32(columns))
}

func (a *App) renderDetailHero(c *ui.Context, d Detail, width float32) {
	posterWidth := float32(168)
	if width < 720 {
		posterWidth = 132
	}
	ui.Column(c.Key("detail-introduction")).FillWidth().Gap(20).Padding(0, 0, 20, 0).Children(func() {
		ui.Row(c.Key("detail-hero-row")).FillWidth().Gap(24).AlignItems(ui.Start).Children(func() {
			a.image(c, d.CoverURL, posterWidth, posterWidth*posterAspectRatio)
			ui.Column(c.Key("detail-hero-info")).Grow(1).MinWidth(0).Gap(12).Children(func() {
				ui.Text(c, d.Title).FontSize(26).Bold().MaxLines(2).Tooltip(d.Title)
				ui.Row(c.Key("detail-stats")).Wrap().Gap(16).AlignItems(ui.Center).Children(func() {
					if d.Heat != "" {
						ui.Text(c, d.Heat).FontSize(13).Bold().TextColor(c.Theme().Accent)
					}
					if d.FollowCount > 0 {
						ui.Text(c, formatCountText(d.FollowCount)+"人在追").FontSize(12).TextColor(c.Theme().TextMuted)
					}
					if d.PlayCount > 0 {
						ui.Text(c, formatCountText(d.PlayCount)+"次播放").FontSize(12).TextColor(c.Theme().TextMuted)
					}
					if d.LikeCount > 0 {
						ui.Text(c, formatCountText(d.LikeCount)+"赞").FontSize(12).TextColor(c.Theme().TextMuted)
					}
				})
				ui.Row(c.Key("detail-tags")).Wrap().Gap(7).Children(func() {
					labels := append(detailProgressPieces(d.StatusText), d.Tags...)
					if d.EpisodeCount > 0 && !strings.Contains(d.StatusText, "集") {
						labels = append([]string{fmt.Sprintf("全%d集", d.EpisodeCount)}, labels...)
					}
					seen := map[string]bool{}
					for _, label := range labels {
						if label != "" && !seen[label] {
							seen[label] = true
							ui.Badge(c, label).FontSize(11).Padding(4, 8).Radius(6).Background(ui.RGBA(255, 255, 255, .045)).TextColor(c.Theme().TextMuted)
						}
					}
				})
				if d.Description != "" {
					a.renderDetailDescription(c, d.Description, max(float32(0), width-posterWidth-24))
				}

				a.renderDetailSummaryRow(c, d, max(float32(0), width-posterWidth-24))
			})
		})
		if len(d.Actors) > 0 {
			a.renderDetailActors(c, d)
		}
	})
}

// This row combines a source summary, actions and date rather than a window
// toolbar (MyGo v0.9.1). Keep the existing PrimaryButton/Button semantics and
// appearance; the source shrinks first, with complete titles in tooltips.
func (a *App) renderDetailSummaryRow(c *ui.Context, d Detail, width float32) {
	row := ui.Row(c.Key("detail-summary-row")).FillWidth().Gap(c.Theme().Space(3)).AlignItems(ui.Center)
	if width < 560*max(float32(1), c.Preferences().TextScale) {
		row.Wrap()
	}
	row.Children(func() {
		if d.Source != nil {
			a.renderDetailSource(c, d.Source).Shrink(1).MaxWidth(min(float32(420), width))
		}
		a.renderDetailActions(c, d).Shrink(0).MaxWidth(width)
		if d.PublishText != "" {
			ui.Text(c.Key("detail-publish-date"), d.PublishText).FontSize(11).SingleLine().Shrink(0).TextColor(ui.RGBA(255, 255, 255, .42))
		}
	})
}

func (a *App) renderDetailActions(c *ui.Context, d Detail) ui.Element {
	return ui.Row(c.Key("detail-actions")).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
		if !d.Upcoming {
			label := "播放 / 继续观看"
			if a.resume.ResumeEpisode > 1 {
				label = fmt.Sprintf("继续观看第 %d 集", a.resume.ResumeEpisode)
			}
			ui.PrimaryButton(c.Key("detail-hero-play"), "").Label(label).Height(38).Padding(0, 16).Radius(9).OnClick(func() { go a.PlayDetail() }).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() { ui.Icon(c, detailPlayIcon).Size(16, 16); ui.Text(c, label).FontSize(13).Bold().SingleLine() })
			})
		}
		label := "收藏"
		if d.Favorite {
			label = "已收藏"
		}
		button := ui.Button(c.Key("detail-hero-fav"), label).Height(38).Padding(0, 14).Radius(9).OnClick(func() { go a.ToggleDetailFavorite() })
		if d.Favorite {
			button.Background(c.Theme().Selection).TextColor(c.Theme().Accent)
		}
		if d.Upcoming || d.Reserved {
			label := "预约"
			if d.Reserved {
				label = "已预约"
			}
			ui.Button(c.Key("detail-hero-res"), label).Height(38).Padding(0, 14).Radius(9).OnClick(func() { go a.ToggleDetailReservation() })
		}
	})
}

func (a *App) renderDetailActors(c *ui.Context, d Detail) {
	ui.ScrollHorizontal(c.Key("detail-actors-flow")).Height(96).Scrollbars(false).Children(func() {
		ui.Row(c).Gap(18).Children(func() {
			for i, actor := range d.Actors {
				actor := actor
				id := actor.UserID
				if id == "" {
					id = actor.ActorID
				}
				content := func() {
					ui.Column(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Avatar(c, actor.Name, nil).Size(40, 40).Background(ui.RGBA(255, 255, 255, .065)).TextColor(c.Theme().TextMuted)
						ui.Text(c, actor.Name).FontSize(12).SingleLine()
						if actor.Role != "" {
							ui.Text(c, actor.Role).FontSize(10).TextColor(c.Theme().TextMuted).SingleLine()
						}
					})
				}
				if id == "" {
					ui.Column(c.Key(fmt.Sprintf("actor:%s:%d", actor.Name, i))).Width(86).Height(92).Padding(4).Children(content)
				} else {
					ui.ButtonBase(c.Key(fmt.Sprintf("actor:%s:%d", actor.Name, i))).Width(86).Height(92).Radius(10).Padding(4).Label("演员主页：" + actor.Name).Tooltip(actor.Name).OnClick(func() { go a.OpenActor(id) }).Children(content)
				}
			}
		})
	})
}

func (a *App) renderDetailTabs(c *ui.Context, d Detail) {
	ui.Row(c.Key("detail-content-tabs")).Height(48).FillWidth().AlignItems(ui.Center).Background(c.Theme().Background).Children(func() {
		count := len(d.Episodes)
		if count == 0 {
			count = d.EpisodeCount
		}
		reviewLabel := "剧评"
		if d.Reviews.Loaded {
			reviewLabel = fmt.Sprintf("剧评 %d", d.Reviews.Total)
		}
		ui.Tabs(c, &a.detailTab, fmt.Sprintf("选集 %d", count), reviewLabel, "相关作品").Label("剧集内容").OnChange(func() {
			if a.detailTab == detailTabReviews {
				go a.LoadDetailReviews(false)
			}
			c.Invalidate()
		})
	})
}

func (a *App) renderDetailEpisodeTools(c *ui.Context, d Detail) {
	ui.Row(c.Key("detail-episode-tools")).FillWidth().Height(42).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Textf(c, "共 %d 集", len(d.Episodes)).FontSize(11).TextColor(c.Theme().TextMuted)
	})
}

func (a *App) renderDetailEpisodeRow(c *ui.Context, episodes []CatalogItem, start, columns int, width float32) {
	ui.Row(c.Key(fmt.Sprintf("episode-row:%d", start))).FillWidth().Height(detailEpisodeHeight + detailEpisodeGap).Gap(detailEpisodeGap).Children(func() {
		for i := start; i < min(start+columns, len(episodes)); i++ {
			ep := episodes[i]
			active := a.resume.ResumeEpisode == ep.EpisodeCount
			button := ui.Button(c.Key(fmt.Sprintf("ep-%d", ep.EpisodeCount)), "").Label(fmt.Sprintf("第%d集", ep.EpisodeCount)).Width(width).Height(detailEpisodeHeight).Padding(10, 12).Radius(9).Border(1, c.Theme().Border).OnClick(func() { go a.ChooseEpisode(ep.EpisodeCount) })
			if active {
				button.Background(c.Theme().Selection).Border(1, c.Theme().Accent.Alpha(.5)).TextColor(c.Theme().Accent)
			} else {
				button.Background(ui.RGBA(255, 255, 255, .035))
			}
			button.Children(func() {
				ui.Textf(c, "第%d集", ep.EpisodeCount).FontSize(12).Grow(1).MinWidth(0).SingleLine()
				if ep.DurationSec > 0 {
					ui.Text(c, fmt.Sprintf("%02d:%02d", ep.DurationSec/60, ep.DurationSec%60)).FontSize(11).TextColor(c.Theme().TextMuted)
				}
			})
		}
	})
}

func detailRelatedSections(d Detail) []string {
	sections := []string{}
	if len(d.Seasons.Items) > 0 || d.Seasons.Error != "" {
		sections = append(sections, "seasons")
	}
	if d.Adaptations.Loading || d.Adaptations.HasMore || d.Adaptations.Error != "" || len(d.Adaptations.Items) > 0 {
		sections = append(sections, "adaptations")
	}
	return sections
}

func (a *App) renderDetailRelated(c *ui.Context, d Detail, section string, width float32) {
	generation := a.generation
	ui.Column(c.Key("detail-related:"+section)).FillWidth().Gap(10).Padding(14, 0, 6, 0).Transition(resultTransition).Children(func() {
		switch section {
		case "seasons":
			a.renderDetailCollection(c, "同系列其他季", d.Seasons, width, func() { go a.RetrySupplement(false) })
		case "adaptations":
			a.renderDetailCollection(c, "同一剧本改编", d.Adaptations, width, func() {
				if d.Adaptations.HasMore {
					go a.loadSupplement(d.ID, generation, true, true)
				} else {
					go a.RetrySupplement(true)
				}
			})

			a.continuePageInView(c, "adaptations-page-end", pageFillVersion{d.ID, d.Adaptations.Cursor, len(d.Adaptations.Items)}, 240,
				d.Adaptations.HasMore && !d.Adaptations.Loading && !d.Adaptations.LoadingMore && d.Adaptations.Error == "", func() { a.loadSupplement(d.ID, generation, true, true) })

		}

	})
}

// A compact source summary belongs beside the introduction. Its maximum width
// keeps a short novel title from stretching a bordered card across the window.
func (a *App) renderDetailSource(c *ui.Context, source *ipc.SourceNovelDTO) ui.Element {
	return ui.Row(c.Key("detail-source-card")).MaxWidth(420).MinWidth(0).Gap(10).Padding(10, 12).Radius(10).
		Role(ui.RoleGroup).Label("原著信息").Background(ui.RGBA(255, 255, 255, .035)).Border(1, c.Theme().Border).Children(func() {
		a.image(c, source.CoverURL, 36, 48)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, "原著小说").FontSize(10).TextColor(c.Theme().Accent)
			ui.Text(c, source.Title).FontSize(13).Bold().SingleLine().Tooltip(source.Title)
			if len(source.Tags) > 0 {
				ui.Text(c, strings.Join(source.Tags, " · ")).FontSize(11).SingleLine().TextColor(c.Theme().TextMuted)
			}
		})
	})
}

// Grid covers related seasons and adaptations (MyGo v0.9.1). Every work wraps
// into the page instead of being hidden in a horizontal shelf. The much larger episode collection remains virtualized by List.
func (a *App) renderDetailCollection(c *ui.Context, label string, value DetailCollection, width float32, retry func()) {
	ui.Column(c.Key("detail-collection:" + label)).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(15).Bold()
			if value.Title != "" && value.Title != label {
				ui.Text(c, value.Title).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().MinWidth(0)
			}
			if len(value.Items) > 0 {
				label := fmt.Sprintf("共 %d 部", len(value.Items))
				if value.HasMore {
					label = fmt.Sprintf("已找到 %d 部", len(value.Items))
				}
				ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
			}
		})
		if value.Error != "" {
			inlineError(c, value.Error, retry)
		}
		gap := float32(16)
		columns := max(1, int((width+gap)/(150*max(1, c.Preferences().TextScale)+gap)))
		cardWidth := (width - float32(columns-1)*gap) / float32(columns)
		ui.Grid(c).Columns(columns).FillWidth().GapX(gap).GapY(22).Children(func() {
			for _, item := range value.Items {
				a.catalogCardSized(c, item, cardWidth)
			}
		})
	})
}

func (a *App) detailSkeleton(c *ui.Context) {
	s := pagePlaceholder(c)
	width := a.gridContentWidth(c)
	columns, cellWidth := detailEpisodeGeometry(width, c.Preferences().TextScale)
	ui.Scroll(c.Key("detail-page-scroll-skeleton")).Label("详情内容").Fill().Scrollbars(false).Gap(20).Children(func() {
		posterWidth := float32(168)
		if width < 720 {
			posterWidth = 132
		}
		ui.Row(c.Key("detail-hero-skeleton")).FillWidth().Gap(24).Children(func() {
			s.bone(c.Key("poster"), 10).Size(posterWidth, posterWidth*posterAspectRatio)
			ui.Column(c).Grow(1).Gap(14).Children(func() {
				s.text(c.Key("title"), 26, 1.2).WidthPercent(64)
				s.text(c.Key("stats"), 13, 1.3).WidthPercent(48)
				s.text(c.Key("tags"), 12, 1.3).WidthPercent(58)
				for i := range 3 {
					s.text(c.Key(i), 13, 1.3).WidthPercent(92 - float32(i)*12)
				}
				ui.Row(c).Gap(10).Children(func() { s.bone(c.Key("play"), 9).Size(158, 38); s.bone(c.Key("favorite"), 9).Size(70, 38) })
			})
		})
		ui.Row(c).Height(48).AlignItems(ui.Center).Gap(24).Children(func() { s.text(c.Key("episodes"), 14, 1.3).Width(72); s.text(c.Key("related"), 14, 1.3).Width(76) })
		for row := range 4 {
			ui.Row(c.Key(row)).Gap(detailEpisodeGap).Children(func() {
				for col := range columns {
					s.bone(c.Key(col), 9).Size(cellWidth, detailEpisodeHeight)
				}
			})
		}
	})
}
