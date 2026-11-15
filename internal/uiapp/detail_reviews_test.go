package uiapp

import (
	"context"
	"errors"
	"fmt"
	"myguo/internal/ipc"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type reviewTestProvider struct {
	calls   atomic.Int32
	read    func(context.Context, string, string) (*ipc.ReviewPageDTO, error)
	replies func(context.Context, string, string, string) (*ipc.CommentPageDTO, error)
}

func (p *reviewTestProvider) GetSeriesReviews(ctx context.Context, id, cursor string) (*ipc.ReviewPageDTO, error) {
	p.calls.Add(1)
	return p.read(ctx, id, cursor)
}
func (p *reviewTestProvider) GetSeriesReviewReplies(ctx context.Context, id, parent, cursor string) (*ipc.CommentPageDTO, error) {
	if p.replies == nil {
		return &ipc.CommentPageDTO{}, nil
	}
	return p.replies(ctx, id, parent, cursor)
}

func reviewFixture(id, text string) ipc.ReviewDTO {
	return ipc.ReviewDTO{CommentDTO: ipc.CommentDTO{ID: id, Author: "观众", Content: text, CreatedAt: 1791854629}, Score: 10, ScoreNote: "观看后点评"}
}
func reviewApp(p detailReviewProvider) *App {
	a := NewTestApp()
	a.view = "detail"
	a.detail = Detail{ID: "12345678", Title: "剧评测试"}
	a.reviewsSource = p
	return a
}

func TestDetailMetadataRefreshPreservesIndependentReviews(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			started, release, reviewDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			p := &reviewTestProvider{read: func(context.Context, string, string) (*ipc.ReviewPageDTO, error) {
				close(started)
				<-release
				return &ipc.ReviewPageDTO{Items: []ipc.ReviewDTO{reviewFixture("one", "已加载剧评")}, Total: 1}, nil
			}}
			a := reviewApp(p)
			a.detail.ID = "123456"
			a.SetInvalidator(func() {})
			var calls atomic.Int32
			metadata := &countingDetailProvider{calls: &calls, block: make(chan struct{})}
			a.detailSource = metadata
			metadataDone := make(chan struct{})
			go func() { a.loadDetail("123456"); close(metadataDone) }()
			metadata.waitStarted(t)
			go func() { a.LoadDetailReviews(false); close(reviewDone) }()
			<-started
			if completed {
				close(release)
				<-reviewDone
			}
			close(metadata.block)
			<-metadataDone
			v := a.DetailState().Reviews
			if completed && (!v.Loaded || len(v.Items) != 1) || !completed && !v.Loading {
				t.Fatalf("metadata replaced independent review state: %+v", v)
			}
			if !completed {
				close(release)
				<-reviewDone
			}
			if v := a.DetailState().Reviews; !v.Loaded || len(v.Items) != 1 {
				t.Fatalf("review result lost: %+v", v)
			}
		})
	}
}

func TestDetailReviewsTabBetweenEpisodesAndRelatedLoadsLazily(t *testing.T) {
	p := &reviewTestProvider{read: func(context.Context, string, string) (*ipc.ReviewPageDTO, error) {
		return &ipc.ReviewPageDTO{Items: []ipc.ReviewDTO{reviewFixture("review-1", "真实剧评内容")}, Total: 911}, nil
	}}
	a := reviewApp(p)
	tt := renderTesterAndAdvance(t, a, 1200, 1000)
	if p.calls.Load() != 0 {
		t.Fatal("reviews requested before opening tab")
	}
	if err := tt.Click("剧评"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool { return a.DetailState().Reviews.Loaded })
	tt.Frame()
	tt.Advance(180)
	a.mu.Lock()
	tab := a.detailTab
	a.mu.Unlock()
	if tab != 1 || !tt.HasText("真实剧评内容") || !tt.HasText("10.0 分") {
		t.Fatalf("review tab or content missing: tab=%d texts=%v", tab, tt.Texts())
	}
	if err := tt.Click("相关作品"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	tab = a.detailTab
	a.mu.Unlock()
	if tab != 2 {
		t.Fatal("related tab must follow reviews")
	}
	if err := tt.Click("剧评 911"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.calls.Load() != 1 {
		t.Fatal("loaded reviews fetched again on tab round trip")
	}
}
func TestDetailReviewsPaginationDeduplicatesAndKeepsItemsOnFailure(t *testing.T) {
	fail := true
	p := &reviewTestProvider{read: func(_ context.Context, _, cursor string) (*ipc.ReviewPageDTO, error) {
		if cursor == "" {
			return &ipc.ReviewPageDTO{Items: []ipc.ReviewDTO{reviewFixture("one", "第一页")}, Total: 2, HasMore: true, NextCursor: "next"}, nil
		}
		if cursor != "next" {
			t.Errorf("wrong review cursor %q", cursor)
		}
		if fail {
			return nil, errors.New("temporary upstream failure")
		}
		return &ipc.ReviewPageDTO{Items: []ipc.ReviewDTO{reviewFixture("one", "重复"), reviewFixture("two", "第二页")}, Total: 2}, nil
	}}
	a := reviewApp(p)
	a.LoadDetailReviews(false)
	a.LoadDetailReviews(true)
	state := a.DetailState().Reviews
	if len(state.Items) != 1 || state.Error == "" || !state.HasMore || state.Cursor != "next" {
		t.Fatalf("failed page erased prior data: %+v", state)
	}
	fail = false
	a.LoadDetailReviews(true)
	state = a.DetailState().Reviews
	if len(state.Items) != 2 || state.Items[1].ID != "two" || state.Error != "" || state.HasMore {
		t.Fatalf("retry/dedupe failed: %+v", state)
	}
}
func TestDetailReviewsDiscardLateResultsAfterNavigation(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	p := &reviewTestProvider{read: func(context.Context, string, string) (*ipc.ReviewPageDTO, error) {
		close(started)
		<-release
		return &ipc.ReviewPageDTO{Items: []ipc.ReviewDTO{reviewFixture("old", "旧剧评")}}, nil
	}}
	a := reviewApp(p)
	go func() { a.LoadDetailReviews(false); close(done) }()
	<-started
	a.OpenDetail("22345678")
	close(release)
	<-done
	state := a.DetailState()
	if state.ID != "22345678" || len(state.Reviews.Items) != 0 || state.Reviews.Loading {
		t.Fatalf("stale reviews crossed series: %+v", state)
	}
}
func TestDetailReviewsLongTextExpandsWithinOneList(t *testing.T) {
	a := reviewApp(nil)
	a.detailTab = detailTabReviews
	a.detail.Reviews = DetailReviews{Loaded: true, Total: 1, Items: []ipc.ReviewDTO{reviewFixture("long", strings.Repeat("这是一段较长的剧评内容。", 50))}}
	tt := renderTesterAndAdvance(t, a, 1000, 1000)
	if !tt.HasText("展开全文") {
		t.Fatal("long review has no expand action")
	}
	if err := tt.Click("展开全文"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.detail.Reviews.expanded["long"] || !tt.HasText("收起全文") {
		t.Fatal("long review did not expand")
	}
}
func TestDetailReviewsEmptyAndFailureRemainDistinct(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "failure"}[failure], func(t *testing.T) {
			p := &reviewTestProvider{read: func(context.Context, string, string) (*ipc.ReviewPageDTO, error) {
				if failure {
					return nil, errors.New("unavailable")
				}
				return &ipc.ReviewPageDTO{}, nil
			}}
			a := reviewApp(p)
			a.detailTab = detailTabReviews
			a.LoadDetailReviews(false)
			tt := renderTesterAndAdvance(t, a, 1000, 900)
			if failure {
				if !tt.HasText("剧评加载失败，请重试") || tt.HasText("暂无剧评") {
					t.Fatal("failure presented as empty")
				}
			} else if !tt.HasText("暂无剧评") {
				t.Fatal("empty reviews missing")
			}
		})
	}
}

func TestDetailReviewRepliesExpandAndReuseLoadedPage(t *testing.T) {
	var calls atomic.Int32
	item := reviewFixture("parent", "父剧评")
	item.ReplyCount = 3
	p := &reviewTestProvider{read: func(context.Context, string, string) (*ipc.ReviewPageDTO, error) {
		return &ipc.ReviewPageDTO{Items: []ipc.ReviewDTO{item}, Total: 1}, nil
	}, replies: func(_ context.Context, id, parent, cursor string) (*ipc.CommentPageDTO, error) {
		if id != "12345678" || parent != "parent" || cursor != "" {
			t.Error("reply scope lost")
		}
		calls.Add(1)
		return &ipc.CommentPageDTO{Items: []ipc.CommentDTO{{ID: "reply", Author: "回复者", Content: "真实回复内容"}}}, nil
	}}
	a := reviewApp(p)
	a.LoadDetailReviews(false)
	a.detailTab = detailTabReviews
	tt := renderTesterAndAdvance(t, a, 1200, 1000)
	if err := tt.Click("3 条回复"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		v := a.detail.Reviews.replies["parent"]
		return v != nil && v.Loaded
	})
	tt.Frame()
	tt.Advance(250)
	if !tt.HasText("真实回复内容") {
		t.Fatalf("reply expansion missing: %v", tt.Texts())
	}
	for i := 0; i < 2; i++ {
		if err := tt.Click("3 条回复"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Advance(250)
	}
	if calls.Load() != 1 {
		t.Fatal("loaded reply page fetched twice")
	}
}
func TestReviewRetryAfterFilteredEmptyPageKeepsContinuation(t *testing.T) {
	fail := true
	p := &reviewTestProvider{read: func(_ context.Context, _, cursor string) (*ipc.ReviewPageDTO, error) {
		if cursor == "" {
			return &ipc.ReviewPageDTO{HasMore: true, NextCursor: "next", Total: 1}, nil
		}
		if fail {
			return nil, errors.New("temporary")
		}
		return &ipc.ReviewPageDTO{Items: []ipc.ReviewDTO{reviewFixture("one", "下一页")}, Total: 1}, nil
	}}
	a := reviewApp(p)
	a.LoadDetailReviews(false)
	a.LoadDetailReviews(true)
	fail = false
	a.detailTab = detailTabReviews
	tt := renderTesterAndAdvance(t, a, 1000, 900)
	if err := tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	waitFor(time.Second, func() bool { return len(a.DetailState().Reviews.Items) == 1 })
	if len(a.DetailState().Reviews.Items) != 1 {
		t.Fatal("empty prior page prevented continuation retry")
	}
}
