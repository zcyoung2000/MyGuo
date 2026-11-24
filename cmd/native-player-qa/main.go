// Native player QA uses the production renderer/controller/AVKit and a local
// SQLite database. Catalog and social responses are fixtures; nothing is sent
// to a cloud account. Supply a local file or a localhost media URL.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/plugins/automation"
	"github.com/egoist/mygo/ui"
	"myguo/internal/avkit"
	"myguo/internal/ipc"
	"myguo/internal/media"
	"myguo/internal/native"
	"myguo/internal/playback"
	"myguo/internal/playerengine"
	"myguo/internal/store"
	"myguo/internal/uiapp"
)

type fixtures struct {
	playback.Backend
	db              *store.Store
	source          string
	landscapeSource string
	fallbackSource  string
	resolveDelay    time.Duration
}

// heldNativeStart deterministically reproduces a decoded, buffered first frame
// whose native clock remains stopped. Only a fresh local asset releases it.
type heldNativeStart struct {
	avkit.Player
	loads atomic.Int32
	held  atomic.Bool
}

func (p *heldNativeStart) Load(url string) error {
	p.loads.Add(1)
	return p.Player.Load(url)
}
func (p *heldNativeStart) Play() {
	if p.loads.Load() == 1 && p.held.Load() {
		return
	}
	p.Player.Play()
}
func (p *heldNativeStart) Snapshot() avkit.Status {
	s := p.Player.Snapshot()
	if p.loads.Load() == 1 && s.Ready {
		p.held.Store(true)
		p.Player.Pause()
		s.Position, s.Playing, s.Buffering = 0, false, false
	}
	return s
}

func (b *fixtures) PlaybackScope() string { return "" }
func (b *fixtures) FlushPendingProgress() {}
func (b *fixtures) SaveProgress(ctx context.Context, id, ep, title, cover string, num, total int, pos, duration float64) error {
	return b.SaveProgressForScope(ctx, "", id, ep, title, cover, num, total, pos, duration)
}
func (b *fixtures) SaveProgressForScope(ctx context.Context, scope, id, ep, title, cover string, num, total int, pos, duration float64) error {
	_, err := b.db.SavePlaybackProgress(store.HistoryItem{SeriesID: id, Title: title, EpisodeNum: num, TotalEpisodes: total, PositionSec: pos, DurationSec: duration}, ep, scope)
	return err
}
func (b *fixtures) PlayEpisode(ctx context.Context, id, ep string, num int, quality string, hls bool) (*ipc.PlaybackDTO, error) {
	if b.resolveDelay > 0 {
		timer := time.NewTimer(b.resolveDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	last := 0.
	if h, ok := b.db.GetHistoryItem(id); ok && h.EpisodeNum == num {
		last = h.PositionSec
	}
	source := b.source
	if b.landscapeSource != "" && id == "710000000000002" {
		source = b.landscapeSource
	}
	dto := &ipc.PlaybackDTO{SeriesID: id, EpisodeID: fmt.Sprintf("%s%02d", id, num), EpisodeNum: num, DurationSec: 90, LastPosition: last, Quality: quality, StreamURL: source, FullStreamURL: source, IsFullCached: true, Qualities: []ipc.QualityOptionDTO{{ID: "720p", Label: "720p", Available: true}, {ID: "1080p", Label: "1080p", Available: true}}}
	if b.fallbackSource != "" {
		dto.HLSStreamURL, dto.FullStreamURL, dto.IsFullCached = source, b.fallbackSource, false
	}
	return dto, nil
}
func (b *fixtures) GetSeriesDetail(ctx context.Context, id string) (*ipc.SeriesDetailDTO, error) {
	episodes := []ipc.EpisodeDTO{}
	for n := 1; n <= 5; n++ {
		episodes = append(episodes, ipc.EpisodeDTO{Number: n, Playable: true})
	}
	return &ipc.SeriesDetailDTO{ID: id, Title: "原生验收 " + id[len(id)-3:], EpisodeCount: 5, Episodes: episodes}, nil
}
func (b *fixtures) GetNextSeason(context.Context, string) (*ipc.SeriesCard, error) { return nil, nil }
func (b *fixtures) GetRecommendFeed(context.Context, string, string) (*ipc.RecommendFeedResult, error) {
	return &ipc.RecommendFeedResult{Items: []ipc.SeriesCard{{ID: "710000000000001"}, {ID: "710000000000002"}, {ID: "710000000000003"}}}, nil
}
func (b *fixtures) RestoreRecommendFeed(context.Context, string) (*ipc.RecommendFeedResult, error) {
	return &ipc.RecommendFeedResult{HasMore: true}, nil
}
func (b *fixtures) ConsumeRecommendFeedItem(context.Context, string, string) error { return nil }
func (b *fixtures) UpdateFeedPreloadWindow(ctx context.Context, v int64, ep, q string, items []media.FeedPreloadItem) ([]media.FeedPreloadStatus, error) {
	out := []media.FeedPreloadStatus{}
	for _, i := range items {
		out = append(out, media.FeedPreloadStatus{EpisodeID: i.EpisodeID, Quality: i.Quality, Complete: true, CachedSeconds: 90})
	}
	return out, nil
}
func (b *fixtures) GetProfile(context.Context) (ipc.UserProfile, error) {
	return ipc.UserProfile{LoggedIn: true, UserID: "local-qa"}, nil
}
func (b *fixtures) IsFavorite(ctx context.Context, id string) (bool, error) {
	return b.db.IsFavorite(id), nil
}
func (b *fixtures) ToggleFavorite(ctx context.Context, id, title, cover, desc string, total int) (bool, error) {
	return b.db.ToggleFavorite(store.FavoriteItem{SeriesID: id, Title: title, TotalEpisodes: total}, "")
}
func (b *fixtures) ToggleLike(ctx context.Context, id, ep, title, cover string, num int) (bool, error) {
	liked := !b.db.IsLiked(id, ep, num)
	return liked, b.db.SetLike(store.LikeItem{SeriesID: id, EpisodeID: ep, EpisodeNum: num, Title: title}, liked, "")
}
func (b *fixtures) GetEpisodeComments(ctx context.Context, id, ep, cursor string) (*ipc.CommentPageDTO, error) {
	if cursor != "" {
		return &ipc.CommentPageDTO{Items: []ipc.CommentDTO{{ID: "second", Author: "本地剧友", Content: "第二页测试评论"}}, Total: 2}, nil
	}
	return &ipc.CommentPageDTO{Items: []ipc.CommentDTO{{ID: "first", Author: "本地剧友", Content: "这是隔离验收中的本地评论，支持展开回复。", ReplyCount: 1}}, HasMore: true, NextCursor: "page2", Total: 2}, nil
}
func (b *fixtures) GetEpisodeCommentReplies(context.Context, string, string, string, string) (*ipc.CommentPageDTO, error) {
	return &ipc.CommentPageDTO{Items: []ipc.CommentDTO{{ID: "reply", Author: "回复者", Content: "本地回复测试"}}}, nil
}
func (b *fixtures) GetDanmaku(ctx context.Context, id, ep string, start, duration int64) (*ipc.DanmakuPage, error) {
	p := &ipc.DanmakuPage{}
	for offset := start; offset < start+duration; offset += 3000 {
		p.Items = append(p.Items, ipc.Danmaku{ID: fmt.Sprint(offset), Text: fmt.Sprintf("原生弹幕互动 %d", offset/1000), TimeMs: offset, Color: "#ffffff", ShowDigg: true, ShowAddOne: true})
	}
	return p, nil
}
func (b *fixtures) SendDanmaku(ctx context.Context, id, ep, text string, at int64, reply string) (*ipc.DanmakuSendResult, error) {
	return &ipc.DanmakuSendResult{ID: fmt.Sprint(time.Now().UnixNano()), UserID: "local-qa", Text: text, TimeMs: at}, nil
}
func (b *fixtures) LikeDanmaku(ctx context.Context, ep, id string, liked bool) (bool, error) {
	return liked, nil
}

func main() {
	source := flag.String("source", "http://127.0.0.1:18764/sample.mp4", "local test media")
	landscapeSource := flag.String("landscape-source", "", "optional second feed item for aspect-ratio QA")
	fallbackSource := flag.String("fallback-source", "", "optional complete source for HLS recovery QA")
	trace := flag.Bool("trace", false, "log playback clock once per second")
	root := flag.String("data", "/tmp/myguo-native-player/qa", "isolated test data")
	exercisePIP := flag.Bool("exercise-pip", false, "verify native PiP handoff and restore")
	exerciseFeed := flag.Bool("exercise-feed", false, "verify native autoplay after paused forward and reverse feed selections")
	exerciseSeek := flag.Bool("exercise-seek", false, "verify paused and playing seeks preserve playback intent")
	exerciseBufferedStart := flag.Bool("exercise-buffered-start", false, "hold the first decoded frame and verify one local HLS reopen recovers it")
	quitAfter := flag.Duration("quit-after", 0, "exercise graceful quit after this duration")
	resolveDelay := flag.Duration("resolve-delay", 0, "delay local source resolution for first-frame material QA")
	flag.Parse()
	if err := os.MkdirAll(*root, 0700); err != nil {
		log.Fatal(err)
	}
	os.Setenv("MYGUO_TEST_NO_LEGACY_IMPORT", "1")
	db, err := store.NewStore(*root)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	b := &fixtures{db: db, source: *source, landscapeSource: *landscapeSource, fallbackSource: *fallbackSource, resolveDelay: *resolveDelay}
	app := uiapp.NewTestApp()
	path := filepath.Join(*root, "native-player.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		p := playback.DefaultPreferences()
		p.Volume = .02
		_ = playback.SavePreferences(path, p)
	}
	factory := playerengine.New
	if *exerciseBufferedStart {
		factory = func() (avkit.Player, error) {
			p, err := playerengine.New()
			if err != nil {
				return nil, err
			}
			return &heldNativeStart{Player: p}, nil
		}
	}
	controller := playback.New(b, factory, path, playback.Hooks{Changed: app.PlaybackChanged, Fullscreen: native.ToggleWindowFullscreen, System: func(s playback.State) { native.SetFeedScrollCapture(s.Mode != playback.Idle && s.Panel == "") }, LibraryChanged: func() {}})
	defer controller.Close()
	host := uiapp.NewNativeMediaHost(controller)
	app.SetMediaHost(host)
	mygo.Theme.SetSource(mygo.ThemeDark)
	mygo.App.WhenReady(func() {
		native.Init()
		w := mygo.NewWindow(mygo.WindowOptions{Title: "原生播放器隔离验收", Width: 1280, Height: 820, MinWidth: 1000, MinHeight: 640, TitleBarStyle: mygo.TitleBarHiddenInset, Transparent: true, BackgroundColor: uiapp.WindowBackgroundColor, Vibrancy: mygo.VibrancySidebar, Content: ui.View(app.Render)})
		native.SetWindow(w)
		native.SetMediaCommandHandler(app.MediaCommand)
		native.SetPictureInPictureHandler(host.PictureInPicture)
		native.SetScrollGestureHandler(func(p native.ScrollGesturePayload) {
			cmd := "next"
			if p.Direction == "up" {
				cmd = "prev"
			}
			host.PlayerControl(cmd, nil)
		})
		native.StartScrollGestureMonitor()
		app.SetInvalidator(w.Invalidate)
		w.OnEnterFullScreen(func() { app.SetPlayerFullscreen(true) })
		w.OnLeaveFullScreen(func() { app.SetPlayerFullscreen(false) })
		var stopping, stopped atomic.Bool
		finish := func() {
			if !stopping.CompareAndSwap(false, true) {
				return
			}
			go func() { controller.Close(); stopped.Store(true); mygo.RunOnMain(func() { mygo.App.Quit() }) }()
		}
		w.OnClose(func(e *mygo.CloseEvent) {
			if !stopped.Load() {
				e.PreventDefault()
				finish()
			}
		})
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if !stopped.Load() {
				e.PreventDefault()
				finish()
			}
		})
		w.OnClosed(native.Cleanup)
		w.Show()
		mygo.App.Focus()
		w.Focus()
		if *quitAfter > 0 {
			time.AfterFunc(*quitAfter, func() { mygo.App.Quit() })
		}
		go app.OpenView("recommend")
		if *exerciseBufferedStart {
			go func() {
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					s := controller.Snapshot()
					if p, ok := s.Engine.(*heldNativeStart); ok && p.loads.Load() == 2 && s.Status.Ready && s.Status.Playing && s.Status.Position > .2 && !s.Loading {
						log.Printf("[qa-start] buffered first-frame recovery passed elapsedMs=%d loads=%d", s.FirstFrameMS, p.loads.Load())
						mygo.App.Quit()
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
				log.Printf("[qa-start] buffered first-frame recovery FAILED: %+v", controller.Snapshot().Status)
				mygo.App.Quit()
			}()
		}
		if *trace {
			go func() {
				for range time.NewTicker(time.Second).C {
					s := controller.Snapshot()
					if s.Playback == nil {
						continue
					}
					log.Printf("[qa-clock] episode=%d position=%.2f duration=%.2f buffered=%.2f paused=%t buffering=%t ended=%t error=%q", s.Playback.EpisodeNum, s.Status.Position, s.Status.Duration, s.Status.Buffered, s.Paused, s.Status.Buffering, s.Status.Ended, s.Error)
				}
			}()
		}
		if *exerciseSeek {
			go func() {
				wait := func(label string, condition func(playback.State) bool) bool {
					deadline := time.Now().Add(8 * time.Second)
					for time.Now().Before(deadline) {
						if condition(controller.Snapshot()) {
							log.Printf("[qa-seek] %s passed", label)
							return true
						}
						time.Sleep(50 * time.Millisecond)
					}
					log.Printf("[qa-seek] %s FAILED: %+v", label, controller.Snapshot().Status)
					return false
				}
				playing := func(s playback.State) bool {
					return s.Status.Ready && s.Status.Playing && !s.Paused && !s.Status.Buffering
				}
				if !wait("initial playback", func(s playback.State) bool { return playing(s) && s.Status.Position > 1 }) {
					return
				}
				controller.Command("pause", nil)
				if !wait("explicit pause", func(s playback.State) bool { return s.Paused && !s.Status.Playing }) {
					return
				}
				controller.Command("seek", 31.25)
				pausedAtTarget := func(s playback.State) bool {
					return s.Paused && !s.Status.Playing && s.Status.Ready && s.Status.Position > 31.2 && s.Status.Position < 31.3
				}
				if !wait("paused seek", pausedAtTarget) {
					return
				}
				time.Sleep(400 * time.Millisecond)
				if !pausedAtTarget(controller.Snapshot()) {
					log.Print("[qa-seek] pause retained FAILED")
					return
				}
				log.Print("[qa-seek] pause retained passed")
				controller.Command("play", nil)
				if !wait("resume at saved target", func(s playback.State) bool { return playing(s) && s.Status.Position > 32 }) {
					return
				}
				controller.Command("seek", 2.0)
				if !wait("playing backward seek", func(s playback.State) bool { return playing(s) && s.Status.Position > 2.1 && s.Status.Position < 5 }) {
					return
				}
				controller.Command("pause", nil)
				if !wait("pause after seek", func(s playback.State) bool { return s.Paused && !s.Status.Playing }) {
					return
				}
				position := controller.Snapshot().Status.Position
				controller.Command("play", nil)
				if !wait("resume without seek", func(s playback.State) bool { return playing(s) && s.Status.Position > position+.2 }) {
					return
				}
				log.Print("[qa-seek] all passed")
				mygo.App.Quit()
			}()
		}
		if *exercisePIP {
			go func() {
				wait := func(label string, condition func(playback.State) bool) bool {
					deadline := time.Now().Add(8 * time.Second)
					for time.Now().Before(deadline) {
						if condition(controller.Snapshot()) {
							log.Printf("[qa] %s passed", label)
							return true
						}
						time.Sleep(50 * time.Millisecond)
					}
					log.Printf("[qa] %s FAILED: %+v", label, controller.Snapshot().Status)
					return false
				}
				if !wait("initial frame", func(s playback.State) bool { return s.Status.Ready && s.Status.Position > 1 }) {
					return
				}
				original := controller.Snapshot().Engine
				controller.Command("pip", nil)
				if !wait("PiP enter", func(s playback.State) bool { return s.Status.PictureInPicture }) {
					return
				}
				controller.Command("next", nil)
				if !wait("PiP next drama", func(s playback.State) bool {
					return s.Book.ID == "710000000000002" && s.Status.Ready && s.Status.PictureInPicture
				}) {
					return
				}
				if controller.Snapshot().Engine != original {
					log.Print("[qa] same native surface FAILED")
					return
				}
				log.Print("[qa] same native surface passed")
				controller.Command("pip", nil)
				if !wait("PiP restore", func(s playback.State) bool { return !s.Status.PictureInPicture && s.Status.Displayed }) {
					return
				}
				controller.Command("pause", nil)
				controller.Command("seek", 31.25)
				wait("paused seek", func(s playback.State) bool {
					return s.Paused && s.Status.Ready && s.Status.Position > 31.2 && s.Status.Position < 31.3
				})
			}()
		}
		if *exerciseFeed {
			go func() {
				wait := func(label string, condition func(playback.State) bool) bool {
					deadline := time.Now().Add(8 * time.Second)
					for time.Now().Before(deadline) {
						if condition(controller.Snapshot()) {
							log.Printf("[qa-feed] %s passed", label)
							return true
						}
						time.Sleep(50 * time.Millisecond)
					}
					log.Printf("[qa-feed] %s FAILED: paused=%t status=%+v", label, controller.Snapshot().Paused, controller.Snapshot().Status)
					return false
				}
				playing := func(s playback.State) bool {
					return s.Status.Ready && s.Status.Playing && !s.Paused && !s.Status.Buffering && s.Status.Position > 1
				}
				if !wait("initial autoplay", playing) {
					return
				}
				for _, step := range []struct{ command, id string }{{"next", "710000000000002"}, {"next", "710000000000003"}, {"prev", "710000000000002"}, {"prev", "710000000000001"}} {
					controller.Command("pause", nil)
					if !wait("explicit pause", func(s playback.State) bool { return s.Paused && !s.Status.Playing }) {
						return
					}
					controller.Command(step.command, nil)
					if !wait(step.command+" autoplay "+step.id, func(s playback.State) bool { return s.Book.ID == step.id && playing(s) }) {
						return
					}
				}
				log.Print("[qa-feed] all autoplay checks passed")
				mygo.App.Quit()
			}()
		}

	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
