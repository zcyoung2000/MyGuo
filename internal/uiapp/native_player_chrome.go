package uiapp

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"myguo/internal/playback"
)

// MyGo v0.9.0 supplies native glass over NativeView, ButtonBase/SliderBase
// semantics and virtual lists. These helpers only compose the player's overlay
// placement and transparent skins; drawers deliberately leave the video full size.
func (a *App) renderNativePlayerHeading(c *ui.Context, s playback.State, bottom, available float32, command func(string, any)) {
	ui.Column(c.Key("player-heading")).Absolute().Left(24).Right(106).Bottom(bottom).Gap(5).PassThrough().Transition(playerChromeTransition).Children(func() {
		title := s.Book.Title
		if title == "" {
			title = ""
		}
		ui.Link(c.Key("player-title"), title, "").Label("查看《"+title+"》详情").Tooltip("查看剧集详情").
			FontSize(18).Bold().SingleLine().AlignSelf(ui.Start).MaxWidth(available-130).
			TextColor(ui.RGB(255, 255, 255)).TextShadow(0, 1, 4, ui.RGBA(0, 0, 0, .8)).Disabled(s.Book.ID == "").
			OnClick(func() {
				if s.Book.ID != "" {
					a.OpenDetail(s.Book.ID)
				}
			})
		if s.Playback != nil {
			label := fmt.Sprintf("第 %d 集", s.Playback.EpisodeNum)
			if s.Book.EpisodeCount > 0 {
				label += fmt.Sprintf(" / 共 %d 集", s.Book.EpisodeCount)
			}
			ui.Text(c, label).FontSize(12).TextColor(ui.RGBA(255, 255, 255, .8)).TextShadow(0, 1, 3, ui.RGBA(0, 0, 0, .8)).PassThrough()
		}
	})
	top := float32(18)
	if s.Mode == playback.Feed && available < 1080 {
		top = 76
	}
	ui.Row(c.Key("player-header")).Absolute().Top(top).Right(18).Gap(8).PassThrough().Transition(playerChromeTransition).Children(func() {
		if s.Mode == playback.Feed {
			playerAction(c, "player-refresh", "刷新", func() { command("refresh", nil) })
		}
	})
	if s.Mode == playback.Series || a.navIndex > 0 {
		ui.Toolbar(c.Key("player-navigation"), func() {
			navigationBack(c, "player-back", true, func() {
				if s.Mode == playback.Series {
					command("close", nil)
				} else {
					go a.NavigateHistory(-1)
				}
			})
		}).Label("播放导航").Absolute().Top(7).Left(16).Height(30).Padding(0).Transition(playerChromeTransition)
	}
}
func (a *App) renderNativePlayerRail(c *ui.Context, s playback.State, command func(string, any)) {
	ui.Column(c.Key("player-rail-position")).Absolute().Right(18).Top(114).Bottom(168).Width(64).Justify(ui.Center).PassThrough().Transition(playerChromeTransition).Children(func() {
		chromeSurface(ui.Column(c.Key("player-rail")).FillWidth().Radius(28).Padding(7, 4).Gap(6).AlignItems(ui.Center)).Children(func() {
			liked := s.Playback != nil && s.Playback.IsLiked
			a.nativeRailAction(c, "player-like", likeIcon, chooseText(liked, "已点赞", "点赞"), liked, func() {
				if a.media != nil && a.media.controller != nil {
					a.media.controller.ToggleLike()
				}
			})
			a.nativeRailAction(c, "player-comments", commentIcon, "评论", false, func() { command("panel", "comments") })
			a.nativeRailAction(c, "player-favorite", favoriteIcon, chooseText(s.Favorite, "已收藏", "收藏"), s.Favorite, func() {
				if a.media != nil && a.media.controller != nil {
					a.media.controller.ToggleFavorite()
				}
			})
			a.nativeRailAction(c, "player-episodes", episodesIcon, "选集", false, func() { command("panel", "episodes") })
			if s.Mode == playback.Feed {
				playerFeedArrow(c, "player-feed-prev", "上一部", true, func() { command("prev", nil) })
				playerFeedArrow(c, "player-feed-next", "下一部", false, func() { command("next", nil) })
			}
		})
	})
}
func (a *App) renderNativePlayerPanel(c *ui.Context, s playback.State, width float32, command func(string, any)) {
	contentSurface(ui.Column(c.Key("player-panel:" + s.Panel)).Absolute().Right(18).Top(116).Bottom(156).Width(width).MinHeight(0).Radius(18).Padding(16).Gap(12).Transition(drawerTransition)).Children(func() {
		title := map[string]string{"episodes": "选集播放", "comments": "评论", "settings": "播放设置", "quality": "画质", "speed": "倍速", "danmaku": "弹幕设置", "danmaku-items": "弹幕互动"}[s.Panel]
		ui.Row(c.Key("player-panel-heading")).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, title).Grow(1).FontSize(16).Bold().TextColor(ui.RGB(255, 255, 255))
			panelAction(c, "player-panel-close", "收起", func() { command("panel", s.Panel) })
		})
		switch s.Panel {
		case "episodes":
			a.renderNativeEpisodes(c, s, command)
		case "comments":
			a.renderNativeComments(c, s)
		case "danmaku-items":
			a.renderNativeDanmakuItems(c, s)
		default:
			a.renderNativePlayerSettings(c, s, command)
		}
	})
}

var playerUpIcon = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="m6 15 6-6 6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
var playerDownIcon = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="m6 9 6 6 6-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`))

func playerFeedArrow(c *ui.Context, key, label string, up bool, action func()) {
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).Focusable().Width(44).Height(30).Radius(15).OnClick(action)
	hoverBackground(b, ui.Transparent, ui.RGBA(255, 255, 255, .12))
	b.Children(func() {
		icon := playerDownIcon
		if up {
			icon = playerUpIcon
		}
		ui.Icon(c, icon).Size(20, 20).TextColor(ui.RGBA(255, 255, 255, .9))
	})
}
