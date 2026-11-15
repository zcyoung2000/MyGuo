// player-probe exercises the actual AVKit surface against a supplied local file
// or MediaServer URL, independently of application data and cloud accounts.
package main

import (
	"context"
	"fmt"
	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/plugins/automation"
	"github.com/egoist/mygo/ui"
	"log"
	"myguo/internal/avkit"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	started := time.Now()
	if len(os.Args) < 2 {
		log.Fatal("usage: player-probe URL")
	}
	var player avkit.Player
	var mu sync.Mutex
	var state avkit.Status
	var message string
	mygo.App.WhenReady(func() {
		var err error
		player, err = avkit.NewPlayer()
		if err != nil {
			log.Fatal(err)
		}
		if err = player.Load(os.Args[1]); err != nil {
			log.Fatal(err)
		}
		rate := 1.0
		for _, arg := range os.Args[2:] {
			if strings.HasPrefix(arg, "--rate=") {
				rate, _ = strconv.ParseFloat(strings.TrimPrefix(arg, "--rate="), 64)
			}
		}
		player.Configure(rate, 0.05, false, "contain")
		for _, arg := range os.Args[2:] {
			if strings.HasPrefix(arg, "--resume=") {
				value, _ := strconv.ParseFloat(strings.TrimPrefix(arg, "--resume="), 64)
				player.Seek(value)
			}
		}
		win := mygo.NewWindow(mygo.WindowOptions{Title: "原生播放器验证", Width: 960, Height: 720, Content: ui.View(func(c *ui.Context) {
			mu.Lock()
			s, m := state, message
			mu.Unlock()
			ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
				ui.NativeView(c.Key("video"), player).Grow(1).FillWidth()
				ui.Row(c).Gap(12).Children(func() {
					ui.Button(c, "播放").OnClick(func() { go player.Play() })
					ui.Button(c, "暂停").OnClick(func() { go player.Pause() })
					ui.Button(c, "跳转 15s").OnClick(func() { go player.Seek(15) })
					ui.Button(c, "画中画").OnClick(func() {
						go func() {
							err := player.TogglePictureInPicture(context.Background())
							mu.Lock()
							message = fmt.Sprint(err)
							mu.Unlock()
							winInvalidate()
						}()
					})
				})
				ui.Text(c, fmt.Sprintf("ready=%v displayed=%v play=%v buffer=%v time=%.2f / %.2f ahead=%.2f pip=%v", s.Ready, s.Displayed, s.Playing, s.Buffering, s.Position, s.Duration, s.Buffered-s.Position, s.PictureInPicture))
				ui.Text(c, m+" "+s.Error)
			})
		})})
		winInvalidate = win.Invalidate
		win.OnClosed(func() { _ = player.Close(); mygo.App.Quit() })
		win.Show()
		win.Focus()
		go func() {
			if slices.Contains(os.Args[2:], "--warm") {
				player.Preroll()
				time.Sleep(2 * time.Second)
				s := player.Snapshot()
				log.Printf("warm-before-play ready=%t buffered=%.2f position=%.2f", s.Ready, s.Buffered, s.Position)
			}
			log.Printf("play-request elapsedMs=%d", time.Since(started).Milliseconds())
			player.Play()
		}()
		go func() {
			pipRequested := false
			firstFrame := false
			firstPlaying := false
			measure := slices.Contains(os.Args[2:], "--measure")
			interval := 250 * time.Millisecond
			if measure {
				interval = 50 * time.Millisecond
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			var count int
			for range ticker.C {
				s := player.Snapshot()
				if !firstPlaying && s.Playing && !s.Buffering && s.Position > 0.01 {
					firstPlaying = true
					log.Printf("first-playing elapsedMs=%d position=%.2f", time.Since(started).Milliseconds(), s.Position)
				}
				if s.Displayed && !firstFrame {
					firstFrame = true
					log.Printf("first-frame elapsedMs=%d", time.Since(started).Milliseconds())
				}
				if measure && time.Since(started) > 12*time.Second {
					mygo.App.Quit()
					return
				}
				count++
				if count == 4 || count%20 == 0 {
					log.Printf("state ready=%t displayed=%t playing=%t buffering=%t position=%.2f ahead=%.2f ended=%t pip=%t error=%q", s.Ready, s.Displayed, s.Playing, s.Buffering, s.Position, s.Buffered-s.Position, s.Ended, s.PictureInPicture, s.Error)
				}
				if slices.Contains(os.Args[2:], "--pip") && s.Displayed && s.Position > 2 && !pipRequested {
					pipRequested = true
					go func() {
						log.Printf("PiP enter: %v", player.TogglePictureInPicture(context.Background()))
						if slices.Contains(os.Args[2:], "--pip-cycle") && player.Snapshot().PictureInPicture {
							time.Sleep(time.Second)
							log.Printf("PiP active: %t", player.Snapshot().PictureInPicture)
							log.Printf("PiP restore: %v", player.TogglePictureInPicture(context.Background()))
							log.Printf("PiP restored active: %t", player.Snapshot().PictureInPicture)
						}
					}()
				}
				mu.Lock()
				state = s
				mu.Unlock()
				win.Invalidate()
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

var winInvalidate = func() {}
