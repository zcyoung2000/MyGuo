package uiapp

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
	"myguo/internal/playback"
	"strings"
	"time"
)

func (a *App) renderNativeEpisodes(c *ui.Context, s playback.State, command func(string, any)) {
	count := len(s.Episodes)
	if count == 0 {
		count = s.Book.EpisodeCount
	}
	if count == 0 {
		return
	}
	ui.Text(c, fmt.Sprintf("共 %d 集", count)).FontSize(12).TextColor(ui.RGBA(255, 255, 255, .6))
	const columns = 4
	ui.List(c.Key("player-episodes-list"), &a.playerEpisodesList, (count+columns-1)/columns, func(row int) {
		ui.Row(c.Key(row)).Gap(6).Padding(0, 0, 6, 0).Children(func() {
			for column := 0; column < columns; column++ {
				index := row*columns + column
				if index >= count {
					ui.Box(c).Grow(1)
					continue
				}
				number := index + 1
				playable := true
				if len(s.Episodes) > index {
					number = s.Episodes[index].Number
					playable = s.Episodes[index].Playable
				}
				label := fmt.Sprint(number)
				active := s.Playback != nil && s.Playback.EpisodeNum == number
				b := panelAction(c, fmt.Sprintf("player-episode:%d", number), label, func() {
					if s.Mode == playback.Feed {
						a.playFeedEpisode(s.Book.ID, number)
					} else {
						command("episode", number)
					}
				}).Grow(1).Height(38).Disabled(!playable)
				if active {
					b.Background(ui.RGBA(254, 44, 85, .24)).Border(1, ui.Hex("#fe2c55"))
				}
			}
		})
	}).Grow(1).FillWidth().MinHeight(0)
}
func (a *App) renderNativeComments(c *ui.Context, s playback.State) {
	host := a.media
	if s.CommentTotal > 0 {
		ui.Text(c, fmt.Sprintf("%s 条评论", formatCountText(s.CommentTotal))).FontSize(12).TextColor(ui.RGBA(255, 255, 255, .6))
	}
	if s.CommentError != "" {
		ui.Text(c, s.CommentError).FontSize(12).TextColor(ui.Hex("#ffb3bd"))
		panelAction(c, "player-comments-retry", "重试", func() {
			if host != nil && host.controller != nil {
				host.controller.Command("panel", "")
				host.controller.Command("panel", "comments")
			}
		})
	}
	if !s.CommentLoading && s.CommentError == "" && len(s.CommentItems) == 0 {
		ui.Text(c, "还没有评论").FontSize(13).TextColor(ui.RGBA(255, 255, 255, .6))
	}
	a.playerCommentsList.NearEnd = 2
	a.playerCommentsList.EndReached = func(int) {
		if s.CommentHasMore && !s.CommentLoading && s.CommentError == "" && host != nil && host.controller != nil {
			host.controller.MoreComments()
		}
	}
	a.playerCommentsList.Key = func(row int) any { return s.CommentItems[row].ID }
	ui.List(c.Key("player-comment-list"), &a.playerCommentsList, len(s.CommentItems), func(row int) {
		item := s.CommentItems[row]
		ui.Column(c.Key(item.ID)).Padding(0, 0, 14, 0).Gap(7).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if avatar := a.Artwork(item.AvatarURL); avatar != nil {
					ui.Image(c, avatar).Size(28, 28).Fit(ui.Cover).Radius(14)
				}
				name := item.Author
				if name == "" {
					name = "剧友"
				}
				ui.Text(c, name).Grow(1).FontSize(12).SingleLine().TextColor(ui.RGBA(255, 255, 255, .7))
				if item.Likes > 0 {
					ui.Text(c, formatCountText(item.Likes)+" 赞").FontSize(11).TextColor(ui.RGBA(255, 255, 255, .45))
				}
			})
			ui.Text(c, item.Content).FontSize(13).LineHeight(1.5).TextColor(ui.RGBA(255, 255, 255, .94)).FillWidth()
			if item.CreatedAt > 0 {
				date := time.Unix(item.CreatedAt, 0)
				if item.CreatedAt > 1e12 {
					date = time.UnixMilli(item.CreatedAt)
				}
				ui.Text(c, date.Format("2006-01-02")).FontSize(10).TextColor(ui.RGBA(255, 255, 255, .4))
			}
			if item.ReplyCount > 0 {
				replies := s.Replies[item.ID]
				for _, reply := range replies.Items {
					renderNativeReply(c, reply)
				}
				if replies.Error != "" {
					ui.Text(c, replies.Error).FontSize(11).TextColor(ui.Hex("#ffb3bd"))
				}
				if len(replies.Items) == 0 || replies.HasMore || replies.Error != "" {
					label := fmt.Sprintf("查看 %d 条回复", item.ReplyCount)
					if len(replies.Items) > 0 {
						label = "更多回复"
					}
					panelAction(c, "player-replies:"+item.ID, label, func() {
						if host != nil && host.controller != nil {
							host.controller.LoadReplies(item.ID)
						}
					}).Disabled(replies.Loading)
				}
			}
			ui.Box(c).Height(1).FillWidth().Background(ui.RGBA(255, 255, 255, .06))
		})
	}).Grow(1).FillWidth().MinHeight(0)
}
func renderNativeReply(c *ui.Context, item ipc.CommentDTO) {
	ui.Column(c.Key("reply:"+item.ID)).Padding(8, 10).Radius(6).Background(ui.RGBA(255, 255, 255, .035)).Gap(4).Children(func() {
		name := item.Author
		if name == "" {
			name = "剧友"
		}
		ui.Text(c, name).FontSize(11).TextColor(ui.RGBA(255, 255, 255, .58))
		ui.Text(c, item.Content).FontSize(12).LineHeight(1.4).TextColor(ui.RGBA(255, 255, 255, .88))
	})
}
func (a *App) renderNativePlayerSettings(c *ui.Context, s playback.State, command func(string, any)) {
	ui.Scroll(c.Key("player-settings-scroll")).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(16).FillWidth().Children(func() {
			if s.Panel == "quality" || s.Panel == "settings" {
				nativeSettingTitle(c, "画质")
				if s.Playback == nil {
					ui.Text(c, "视频加载后可选择画质").FontSize(12)
				} else {
					ui.Column(c).Gap(6).Children(func() {
						for _, q := range s.Playback.Qualities {
							quality := q
							label := quality.Label
							if label == "" {
								label = quality.ID
							}
							b := panelAction(c, "player-quality:"+quality.ID, label, func() { command("quality", quality.ID) }).FillWidth().Disabled(!quality.Available)
							if quality.ID == s.Preferences.Quality {
								b.Border(1, ui.RGBA(254, 44, 85, .7))
							}
						}
					})
				}
			}
			if s.Panel == "speed" || s.Panel == "settings" {
				nativeSettingTitle(c, "播放速度")
				ui.Row(c).Wrap().Gap(8).Children(func() {
					for _, rate := range []float64{.75, 1, 1.25, 1.5, 2} {
						value := rate
						b := panelAction(c, fmt.Sprintf("player-speed:%g", rate), fmt.Sprintf("%g×", rate), func() { command("speed", value) })
						if rate == s.Preferences.Rate {
							b.Border(1, ui.RGBA(254, 44, 85, .7))
						}
					}
				})
			}
			if s.Panel == "settings" {
				nativeSettingTitle(c, "声音")
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					panelAction(c, "player-mute", chooseText(s.Preferences.Muted, "取消静音", "静音"), func() { command("mute", nil) })
					playerSlider(c, "player-volume", "音量", s.Preferences.Volume, 1, 0, func(v float64) { command("volume", v) }).Grow(1)
				})
				nativeSettingTitle(c, "画面")
				ui.Row(c).Gap(8).Children(func() {
					panelAction(c, "player-fit-contain", "完整显示", func() { command("fit", "contain") })
					panelAction(c, "player-fit-cover", "铺满画面", func() { command("fit", "cover") })
				})
			}
			if s.Panel == "danmaku" || s.Panel == "settings" {
				nativeSettingTitle(c, "弹幕")
				p := s.Preferences
				enabled := p.Danmaku.Enabled
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "显示弹幕").Grow(1).FontSize(12).TextColor(ui.RGBA(255, 255, 255, .8))
					ui.Switch(c.Key("player-danmaku-enabled"), &enabled).Label("显示弹幕").OnChange(func() { next := p; next.Danmaku.Enabled = enabled; command("preferences", next) })
				})
				ui.Text(c, "透明度").FontSize(12).TextColor(ui.RGBA(255, 255, 255, .7))
				playerSlider(c, "player-danmaku-opacity", "弹幕透明度", p.Danmaku.Opacity, 1, 0, func(v float64) { next := p; next.Danmaku.Opacity = v; command("preferences", next) }).FillWidth()
				ui.Text(c, "字号").FontSize(12).TextColor(ui.RGBA(255, 255, 255, .7))
				playerSlider(c, "player-danmaku-font", "弹幕字号", p.Danmaku.FontSize, 40, 0, func(v float64) { next := p; next.Danmaku.FontSize = v; command("preferences", next) }).FillWidth()
				ui.Text(c, "字重").FontSize(12).TextColor(ui.RGBA(255, 255, 255, .7))
				ui.Row(c).Gap(8).Children(func() {
					for _, weight := range []int{400, 600, 900} {
						value := weight
						label := map[int]string{400: "常规", 600: "加粗", 900: "特粗"}[weight]
						panelAction(c, fmt.Sprintf("player-danmaku-weight:%d", weight), label, func() { next := p; next.Danmaku.FontWeight = value; command("preferences", next) })
					}
				})
				outline := p.Danmaku.Outline
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, "弹幕描边").Grow(1).FontSize(12).TextColor(ui.RGBA(255, 255, 255, .8))
					ui.Switch(c.Key("player-danmaku-outline"), &outline).Label("弹幕描边").OnChange(func() { next := p; next.Danmaku.Outline = outline; command("preferences", next) })
				})
				ui.Text(c, "行数（0 为自动）").FontSize(12).TextColor(ui.RGBA(255, 255, 255, .7))
				playerSlider(c, "player-danmaku-rows", "弹幕行数", float64(p.Danmaku.Rows), 15, 0, func(v float64) { next := p; next.Danmaku.Rows = int(v); command("preferences", next) }).FillWidth()
				panelAction(c, "player-danmaku-interactions", "查看弹幕与互动", func() { command("panel", "danmaku-items") })
				if s.DanmakuError != "" {
					ui.Text(c, s.DanmakuError).FontSize(11).TextColor(ui.RGBA(255, 255, 255, .6))
				}
			}
		})
	})
}
func nativeSettingTitle(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(13).Bold().TextColor(ui.RGB(255, 255, 255))
}
func (a *App) renderNativeDanmakuItems(c *ui.Context, s playback.State) {
	items := s.DanmakuItems
	if len(items) == 0 {
		ui.Text(c, "当前时间段还没有弹幕").FontSize(12).TextColor(ui.RGBA(255, 255, 255, .6))
		return
	}
	ui.List(c.Key("player-danmaku-list"), &a.playerDanmakuList, len(items), func(row int) {
		item := items[row]
		ui.Column(c.Key(item.ID)).Gap(7).Padding(0, 0, 12, 0).Children(func() {
			ui.Text(c, item.Text).FontSize(13).LineHeight(1.4).TextColor(ui.RGB(255, 255, 255))
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, playerTime(float64(item.TimeMs)/1000)).FontSize(10).TextColor(ui.RGBA(255, 255, 255, .5))
				if item.ShowDigg {
					label := "赞"
					if item.Liked {
						label = "已赞"
					}
					if item.LikeCount > 0 {
						label += " " + formatCountText(item.LikeCount)
					}
					panelAction(c, "danmaku-like:"+item.ID, label, func() {
						if a.media != nil && a.media.controller != nil {
							a.media.controller.LikeDanmaku(item.ID, !item.Liked)
						}
					})
				}
				if item.ShowAddOne {
					panelAction(c, "danmaku-plus:"+item.ID, "+1", func() {
						if a.media != nil && a.media.controller != nil {
							a.media.controller.SendDanmaku(strings.TrimSpace(item.Text), item.ID)
						}
					})
				}
			})
		})
	}).Grow(1).FillWidth().MinHeight(0)
}
