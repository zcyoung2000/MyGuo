package playback

import (
	"fmt"
	"math"
	"myguo/internal/ipc"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func (c *Controller) ToggleFavorite() {
	c.post(func() {
		s := c.current
		if s == nil || c.socialBusy["favorite:"+s.seriesID] {
			return
		}
		c.socialBusy["favorite:"+s.seriesID] = true
		scene, ctx, card := c.scene, c.root, s.card
		go func() {
			favorite, err := c.backend.ToggleFavorite(ctx, card.ID, card.Title, card.CoverURL, card.Description, card.EpisodeCount)
			c.post(func() {
				if c.scene != scene {
					return
				}
				delete(c.socialBusy, "favorite:"+s.seriesID)
				if err != nil {
					c.state.Notice = "收藏失败，请重试"
					return
				}
				if c.current != nil && c.current.seriesID == s.seriesID {
					c.state.Favorite = favorite
				}
				if c.hooks.LibraryChanged != nil {
					c.hooks.LibraryChanged()
				}
			})
		}()
	})
}
func (c *Controller) ToggleLike() {
	c.post(func() {
		s := c.current
		if s == nil || s.dto == nil || c.socialBusy["like:"+s.dto.EpisodeID] {
			return
		}
		c.socialBusy["like:"+s.dto.EpisodeID] = true
		scene, ctx, card, dto := c.scene, c.root, s.card, *s.dto
		go func() {
			liked, err := c.backend.ToggleLike(ctx, dto.SeriesID, dto.EpisodeID, card.Title, card.CoverURL, dto.EpisodeNum)
			c.post(func() {
				if c.scene != scene {
					return
				}
				delete(c.socialBusy, "like:"+dto.EpisodeID)
				if err != nil {
					c.state.Notice = "点赞失败，请重试"
					return
				}
				for _, candidate := range c.slots {
					if candidate.dto != nil && candidate.dto.EpisodeID == dto.EpisodeID {
						copy := *candidate.dto
						copy.IsLiked = liked
						candidate.dto = &copy
						if c.current == candidate {
							c.state.Playback = candidate.dto
						}
					}
				}
				if c.hooks.LibraryChanged != nil {
					c.hooks.LibraryChanged()
				}
			})
		}()
	})
}
func (c *Controller) MoreComments() { c.post(func() { c.loadComments(true) }) }
func (c *Controller) loadComments(more bool) {
	s := c.current
	if s == nil || s.dto == nil || c.state.CommentLoading || (more && !c.state.CommentHasMore) {
		return
	}
	c.state.CommentLoading = true
	c.state.CommentError = ""
	cursor := ""
	if more {
		cursor = c.commentCursor
	}
	scene, key, selection, ctx := c.scene, c.desired, c.uiSelection, c.root
	id, ep := s.seriesID, s.dto.EpisodeID
	go func() {
		page, err := c.backend.GetEpisodeComments(ctx, id, ep, cursor)
		c.post(func() {
			if c.scene != scene || c.desired != key || c.uiSelection != selection {
				return
			}
			c.state.CommentLoading = false
			if err != nil || page == nil {
				c.state.CommentError = "评论加载失败，请重试"
				return
			}
			if !more {
				c.state.CommentItems = nil
				c.commentSeen = map[string]bool{}
				c.commentCursors = map[string]bool{}
			}
			for _, item := range page.Items {
				if !c.commentSeen[item.ID] {
					c.commentSeen[item.ID] = true
					c.state.CommentItems = append(c.state.CommentItems, item)
				}
			}
			c.state.CommentTotal = page.Total
			next := page.NextCursor
			if next == "" {
				next = page.Cursor
			}
			c.state.CommentHasMore = page.HasMore && next != "" && next != cursor && !c.commentCursors[next]
			if next != "" {
				c.commentCursors[next] = true
			}
			c.commentCursor = next
		})
	}()
}
func (c *Controller) LoadReplies(commentID string) {
	c.post(func() {
		s := c.current
		if s == nil || s.dto == nil {
			return
		}
		existing := c.state.Replies[commentID]
		if existing.Loading || (len(existing.Items) > 0 && !existing.HasMore) {
			return
		}
		existing.Loading = true
		existing.Error = ""
		c.state.Replies[commentID] = existing
		scene, key, selection, ctx := c.scene, c.desired, c.uiSelection, c.root
		id, ep := s.seriesID, s.dto.EpisodeID
		go func() {
			page, err := c.backend.GetEpisodeCommentReplies(ctx, id, ep, commentID, existing.Cursor)
			c.post(func() {
				if c.scene != scene || c.desired != key || c.uiSelection != selection {
					return
				}
				value := c.state.Replies[commentID]
				value.Loading = false
				if err != nil || page == nil {
					value.Error = "回复加载失败，请重试"
				} else {
					seen := map[string]bool{}
					for _, v := range value.Items {
						seen[v.ID] = true
					}
					for _, v := range page.Items {
						if !seen[v.ID] {
							seen[v.ID] = true
							value.Items = append(value.Items, v)
						}
					}
					next := page.NextCursor
					if next == "" {
						next = page.Cursor
					}
					if c.replyCursors[commentID] == nil {
						c.replyCursors[commentID] = map[string]bool{}
					}
					value.HasMore = page.HasMore && next != "" && next != existing.Cursor && !c.replyCursors[commentID][next]
					if next != "" {
						c.replyCursors[commentID][next] = true
					}
					value.Cursor = next
				}
				c.state.Replies[commentID] = value
			})
		}()
	})
}
func (c *Controller) fetchDanmaku() {
	s := c.current
	if s == nil || s.dto == nil || !c.state.Preferences.Danmaku.Enabled {
		return
	}
	position := int64(math.Max(0, c.state.Status.Position) * 1000)
	start := position / 30000 * 30000
	c.fetchDanmakuWindow(s, start)
	if position-start >= 27000 {
		c.fetchDanmakuWindow(s, start+30000)
	}
}
func (c *Controller) fetchDanmakuWindow(s *slot, start int64) {
	if c.danmakuWindows[start] || c.danmakuLoading[start] {
		return
	}
	c.danmakuLoading[start] = true
	scene, key, selection, ctx := c.scene, c.desired, c.uiSelection, c.root
	id, ep := s.seriesID, s.dto.EpisodeID
	go func() {
		page, err := c.backend.GetDanmaku(ctx, id, ep, start, 30000)
		c.post(func() {
			if c.scene != scene || c.desired != key || c.uiSelection != selection {
				return
			}
			delete(c.danmakuLoading, start)
			if err != nil || page == nil {
				c.state.DanmakuError = "弹幕暂时不可用，可稍后重试"
				// Retry without flooding a failed endpoint on every playback tick.
				c.danmakuWindows[start] = true
				time.AfterFunc(3*time.Second, func() {
					c.post(func() {
						if c.scene == scene && c.desired == key && c.uiSelection == selection {
							delete(c.danmakuWindows, start)
						}
					})
				})
				return
			}
			c.danmakuWindows[start] = true
			c.state.DanmakuError = ""
			seen := map[string]bool{}
			for _, item := range c.state.DanmakuItems {
				seen[item.ID] = true
			}
			cutoff := int64(c.state.Status.Position*1000) - 120000
			for window := range c.danmakuWindows {
				if window+30000 < cutoff {
					delete(c.danmakuWindows, window)
				}
			}
			items := make([]ipc.Danmaku, 0, len(c.state.DanmakuItems)+len(page.Items))
			for _, item := range c.state.DanmakuItems {
				if item.TimeMs >= cutoff {
					items = append(items, item)
				}
			}
			for _, item := range page.Items {
				if !seen[item.ID] && item.Text != "" {
					seen[item.ID] = true
					items = append(items, item)
				}
			}
			sort.SliceStable(items, func(i, j int) bool {
				if items[i].TimeMs == items[j].TimeMs {
					return items[i].ID < items[j].ID
				}
				return items[i].TimeMs < items[j].TimeMs
			})
			c.state.DanmakuItems = items
			c.state.DanmakuVersion++
		})
	}()
}
func (c *Controller) SendDanmaku(text, replyTo string) {
	c.post(func() {
		s := c.current
		if s == nil || s.dto == nil || c.state.Sending {
			return
		}
		text = strings.TrimSpace(text)
		if text == "" {
			c.state.Notice = "请先输入弹幕内容"
			return
		}
		if utf8.RuneCountInString(text) > 200 {
			c.state.Notice = "弹幕最多 200 个字"
			return
		}
		profile, err := c.backend.GetProfile(c.root)
		if err != nil || !profile.LoggedIn {
			c.state.Notice = "请先在设置中登录后发送弹幕"
			return
		}
		c.state.Sending = true
		scene, key, selection, ctx, position := c.scene, c.desired, c.uiSelection, c.root, int64(c.state.Status.Position*1000)
		id, ep := s.seriesID, s.dto.EpisodeID
		go func() {
			result, err := c.backend.SendDanmaku(ctx, id, ep, text, position, replyTo)
			c.post(func() {
				if c.scene != scene || c.desired != key || c.uiSelection != selection {
					return
				}
				c.state.Sending = false
				if err != nil || result == nil {
					c.state.Notice = "弹幕发送失败，请重试"
					return
				}
				id := result.ID
				if id == "" {
					id = fmt.Sprintf("local:%d", time.Now().UnixNano())
				}
				c.state.DanmakuItems = append(c.state.DanmakuItems, ipc.Danmaku{ID: id, UserID: result.UserID, Text: text, TimeMs: position, Color: "#ffffff", ShowDigg: true, ShowAddOne: true})
				c.state.DanmakuVersion++
				c.state.Notice = "弹幕已发送"
				c.sentSequence++
				c.state.SentSequence = c.sentSequence
				c.state.SentText = text
			})
		}()
	})
}
func (c *Controller) LikeDanmaku(id string, liked bool) {
	c.post(func() {
		s := c.current
		if s == nil || s.dto == nil || c.socialBusy["danmaku:"+id] {
			return
		}
		c.socialBusy["danmaku:"+id] = true
		scene, key, selection, ctx := c.scene, c.desired, c.uiSelection, c.root
		ep := s.dto.EpisodeID
		go func() {
			value, err := c.backend.LikeDanmaku(ctx, ep, id, liked)
			c.post(func() {
				if c.scene != scene {
					return
				}
				delete(c.socialBusy, "danmaku:"+id)
				if c.desired != key || c.uiSelection != selection {
					return
				}
				if err != nil {
					c.state.Notice = "弹幕点赞失败，请确认已登录后重试"
					return
				}
				items := append([]ipc.Danmaku(nil), c.state.DanmakuItems...)
				for i := range items {
					if items[i].ID == id {
						if items[i].Liked != value {
							if value {
								items[i].LikeCount++
							} else {
								items[i].LikeCount = max(0, items[i].LikeCount-1)
							}
						}
						items[i].Liked = value
					}
				}
				c.state.DanmakuItems = items
				c.state.DanmakuVersion++
			})
		}()
	})
}
