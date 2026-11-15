package uiapp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

const (
	detailTabEpisodes = iota
	detailTabReviews
	detailTabRelated
)

type detailReviewProvider interface {
	GetSeriesReviews(context.Context, string, string) (*ipc.ReviewPageDTO, error)
}
type detailReviewReplyProvider interface {
	GetSeriesReviewReplies(context.Context, string, string, string) (*ipc.CommentPageDTO, error)
}
type DetailReviews struct {
	Items                                 []ipc.ReviewDTO
	Total                                 int64
	Score                                 float64
	ScoreCount                            int64
	Cursor                                string
	HasMore, Loaded, Loading, LoadingMore bool
	Error                                 string
	seenCursors                           map[string]bool
	emptyPages                            int
	expanded                              map[string]bool
	replies                               map[string]*detailReviewReplies
}
type detailReviewReplies struct {
	Items                          []ipc.CommentDTO
	Cursor                         string
	HasMore, Loaded, Loading, Open bool
	Error                          string
	seenCursors                    map[string]bool
}

func (a *App) reviewProvider() detailReviewProvider {
	if a.reviewsSource != nil {
		return a.reviewsSource
	}
	if p, ok := a.featuresSource.(detailReviewProvider); ok {
		return p
	}
	if a.service != nil {
		return a.service
	}
	return nil
}

func (a *App) LoadDetailReviews(more bool) {
	a.mu.Lock()
	id, generation := a.detail.ID, a.generation
	v := &a.detail.Reviews
	if a.view != "detail" || id == "" || v.Loading || v.LoadingMore || (!more && v.Loaded) || (more && !v.HasMore) {
		a.mu.Unlock()
		return
	}
	cursor := ""
	if more {
		cursor = v.Cursor
		v.LoadingMore = true
	} else {
		v.Loading = true
	}
	v.Error = ""
	p := a.reviewProvider()
	a.invalidate()
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	var page *ipc.ReviewPageDTO
	var err error
	if p == nil {
		err = fmt.Errorf("剧评服务尚未连接")
	} else {
		page, err = p.GetSeriesReviews(ctx, id, cursor)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.generation != generation || a.detail.ID != id {
		return
	}
	v = &a.detail.Reviews
	v.Loading, v.LoadingMore = false, false
	if err != nil || page == nil {
		v.Error = "剧评加载失败，请重试"
		log.Printf("[detail-reviews] read failed: %v", err)
		a.invalidate()
		return
	}
	if !more {
		v.Items = nil
		v.seenCursors = nil
	}
	seen := make(map[string]bool, len(v.Items)+len(page.Items))
	for _, item := range v.Items {
		seen[item.ID] = true
	}
	for _, item := range page.Items {
		if item.ID != "" && !seen[item.ID] {
			seen[item.ID] = true
			v.Items = append(v.Items, item)
		}
	}
	if len(page.Items) == 0 && page.HasMore {
		v.emptyPages++
	} else {
		v.emptyPages = 0
	}
	v.Loaded, v.Total = true, page.Total
	if !more {
		v.Score, v.ScoreCount = page.Score, page.ScoreCount
	}
	v.HasMore = nextCursor(&v.seenCursors, cursor, page.NextCursor, page.HasMore, more)
	v.Cursor = page.NextCursor
	if page.HasMore && !v.HasMore {
		v.Error = "剧评分页异常，请重新打开详情"
	}
	if v.emptyPages >= maxEmptyPages {
		v.Error = "暂时无法取得可展示的剧评，请重试"
	}
	a.invalidate()
}

func (a *App) loadDetailReviewReplies(reviewID string, more bool) {
	a.mu.Lock()
	id, generation := a.detail.ID, a.generation
	v := a.detail.Reviews.replies[reviewID]
	p, ok := a.reviewProvider().(detailReviewReplyProvider)
	if !ok || v == nil || v.Loading || (!more && v.Loaded) || (more && !v.HasMore) {
		a.mu.Unlock()
		return
	}
	cursor := ""
	if more {
		cursor = v.Cursor
	}
	v.Loading, v.Error = true, ""
	a.invalidate()
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	page, err := p.GetSeriesReviewReplies(ctx, id, reviewID, cursor)
	a.mu.Lock()
	defer a.mu.Unlock()
	if generation != a.generation || a.detail.ID != id || a.detail.Reviews.replies[reviewID] != v {
		return
	}
	v.Loading = false
	if err != nil || page == nil {
		v.Error = "回复加载失败，请重试"
		log.Printf("[detail-reviews] replies failed: %v", err)
		a.invalidate()
		return
	}
	if !more {
		v.Items = nil
		v.seenCursors = nil
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		seen[item.ID] = true
	}
	for _, item := range page.Items {
		if item.ID != "" && !seen[item.ID] {
			seen[item.ID] = true
			v.Items = append(v.Items, item)
		}
	}
	v.Loaded = true
	v.Cursor = page.NextCursor
	v.HasMore = nextCursor(&v.seenCursors, cursor, page.NextCursor, page.HasMore, more)
	if page.HasMore && !v.HasMore {
		v.Error = "回复分页异常，请重新展开剧评"
	}
	a.invalidate()
}

func (a *App) renderDetailReviewSummary(c *ui.Context, d Detail) {
	v := d.Reviews
	ui.Row(c.Key("detail-review-summary")).FillWidth().Padding(14, 0).Gap(12).AlignItems(ui.Center).Children(func() {
		if v.Loaded {
			ui.Textf(c, "共 %d 条剧评", v.Total).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		if v.ScoreCount > 0 {
			ui.Textf(c, "%.1f 分 · %d 人评分", v.Score, v.ScoreCount).FontSize(13).TextColor(c.Theme().Accent)
		}
	})
}

// Reviews share the detail page's virtual List. Avatar and Collapsible provide
// native semantics; a read-only numeric score avoids Rating's editing behavior
// (MyGo v0.9.0). Expanded replies grow the same row, without another scroller.
func (a *App) renderDetailReview(c *ui.Context, item ipc.ReviewDTO) {
	v := &a.detail.Reviews
	if v.expanded == nil {
		v.expanded = map[string]bool{}
	}
	if v.replies == nil {
		v.replies = map[string]*detailReviewReplies{}
	}
	ui.Column(c.Key("review:"+item.ID)).FillWidth().Gap(10).Padding(16, 0).Transition(resultTransition).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			avatar, _ := a.Artwork(item.AvatarURL).(*ui.Bitmap)
			ui.Avatar(c, item.Author, avatar).Size(30, 30)
			ui.Text(c, item.Author).FontSize(12).Bold().Grow(1).MinWidth(0).SingleLine()
			if item.Score > 0 {
				ui.Textf(c, "%.1f 分", item.Score).FontSize(12).TextColor(c.Theme().Accent)
			}
		})
		text := ui.Text(c, item.Content).FontSize(13).FillWidth()
		long := utf8.RuneCountInString(item.Content) > 240 || strings.Count(item.Content, "\n") > 5
		if long && !v.expanded[item.ID] {
			text.MaxLines(6)
		}
		if long {
			label := "展开全文"
			if v.expanded[item.ID] {
				label = "收起全文"
			}
			ui.Button(c.Key("review-text:"+item.ID), label).Padding(4, 0).FontSize(11).AlignSelf(ui.Start).OnClick(func() {
				v.expanded[item.ID] = !v.expanded[item.ID]
				if a.detailEpisodesList != nil {
					a.detailEpisodesList.HoldPlace()
				}
				c.Invalidate()
			})
		}
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			if item.CreatedAt > 0 {
				ui.Text(c, time.Unix(item.CreatedAt, 0).Format("2006-01-02")).FontSize(10).TextColor(c.Theme().TextMuted)
			}
			if item.ScoreNote != "" {
				ui.Text(c, item.ScoreNote).FontSize(10).TextColor(c.Theme().TextMuted)
			}
			if item.Likes > 0 {
				ui.Textf(c, "%d 人赞同", item.Likes).FontSize(10).TextColor(c.Theme().TextMuted)
			}
		})
		if item.ReplyCount > 0 {
			if _, ok := a.reviewProvider().(detailReviewReplyProvider); ok {
				r := v.replies[item.ID]
				if r == nil {
					r = &detailReviewReplies{}
					v.replies[item.ID] = r
				}
				ui.Collapsible(c.Key("review-replies:"+item.ID), fmt.Sprintf("%d 条回复", item.ReplyCount), &r.Open, func() {
					if r.Error != "" {
						inlineError(c, r.Error, func() { go a.loadDetailReviewReplies(item.ID, r.Loaded) })
					}
					for _, reply := range r.Items {
						ui.Column(c.Key(reply.ID)).FillWidth().Gap(5).Padding(10).Radius(8).Background(ui.RGBA(255, 255, 255, .025)).Children(func() {
							ui.Text(c, reply.Author).FontSize(11).Bold()
							ui.Text(c, reply.Content).FontSize(12).FillWidth()
						})
					}
					if r.Loaded && len(r.Items) == 0 && !r.Loading && !r.HasMore {
						ui.Text(c, "暂无回复").FontSize(11).TextColor(c.Theme().TextMuted)
					}
					if r.HasMore && !r.Loading {
						ui.Button(c, "更多回复").AlignSelf(ui.Start).OnClick(func() { go a.loadDetailReviewReplies(item.ID, true) })
					}
				}).OnChange(func() {
					if r.Open {
						go a.loadDetailReviewReplies(item.ID, false)
					}
					if a.detailEpisodesList != nil {
						a.detailEpisodesList.HoldPlace()
					}
					c.Invalidate()
				})
			}
		}
		ui.Divider(c)
	})
}

func (a *App) renderDetailReviewFooter(c *ui.Context, d Detail) {
	v := d.Reviews
	ui.Column(c.Key("detail-reviews-footer")).FillWidth().Gap(12).Padding(16, 0).Children(func() {
		if v.Error != "" {
			inlineError(c, v.Error, func() { go a.LoadDetailReviews(v.Loaded) })
		}
		if v.Loaded && len(v.Items) == 0 && !v.Loading && v.Error == "" && !v.HasMore {
			a.emptyView(c, "暂无剧评", "")
		}

		a.continuePageInView(c, "reviews-page-end", pageFillVersion{d.ID, v.Cursor, len(v.Items)}, 0,
			a.detailScroll.MaxY <= 0 && v.Loaded && v.HasMore && !v.Loading && !v.LoadingMore && v.Error == "", func() { a.LoadDetailReviews(true) })

	})
}
