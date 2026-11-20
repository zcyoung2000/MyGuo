package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"myguo/internal/appdirs"
	"myguo/internal/auth"
	"myguo/internal/avkit"
	"myguo/internal/hgapi"
	"myguo/internal/ipc"
	"myguo/internal/media"
	"myguo/internal/native"
	"myguo/internal/playback"
	"myguo/internal/resourcelog"
	"myguo/internal/sparedevice"
	userstore "myguo/internal/store"
	"myguo/internal/uiapp"
	"myguo/internal/upstream"
)

var initialView = "recommend"
var mainWindowTitle = appdirs.AppName

func main() {
	started := time.Now()
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("failed to get user home: %v", err)
	}

	appDir, err := appdirs.AppSupportDir(home)
	if err != nil {
		log.Fatalf("failed to prepare app dir: %v", err)
	}
	if err := os.MkdirAll(appDir, 0755); err != nil {
		log.Fatalf("failed to create app dir: %v", err)
	}

	// GUI launches discard stdout/stderr, so retain backend diagnostics on disk.
	if logFile, err := resourcelog.OpenRotated(
		filepath.Join(appDir, "app.log"),
		resourcelog.DefaultMaxLogBytes,
		resourcelog.DefaultMaxLogFiles,
	); err == nil {
		defer logFile.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("[startup] stage=process-init pid=%d elapsedMs=%d", os.Getpid(), time.Since(started).Milliseconds())

	// 1. Initialize SQLite first; account and anonymous-device identities live
	// beside user data in the same permissioned database.
	store, err := userstore.NewStore(appDir)
	if err != nil {
		log.Fatalf("failed to init store: %v", err)
	}
	defer store.Close()
	log.Printf("[startup] stage=store-ready elapsedMs=%d", time.Since(started).Milliseconds())

	profileStore := store.ProfileStore()
	hostPicker := upstream.NewHostPicker(store.UpstreamStateStore())
	if err := hostPicker.Restore(); err != nil {
		log.Printf("[upstream] host state unavailable: %v", err)
	}
	hostPicker.Configure(
		upstream.DefaultSuccessGain,
		upstream.DefaultFailurePenalty,
		upstream.DefaultMaxHealth,
		upstream.DefaultMinHealth,
		upstream.DefaultBadThreshold,
		upstream.CooldownFromEnvironment(),
		upstream.HalfOpenFromEnvironment(),
		upstream.StaleAgeFromEnvironment(),
	)
	hostTransport := upstream.NewTransport(hostPicker, http.DefaultTransport)
	hostTransport.SetEnabled(upstream.EnabledFromEnvironment())
	hostTransport.SetMaxAttempts(upstream.AttemptsFromEnvironment())
	authenticatedTransport := auth.New()
	authenticatedTransport.SetHTTPClient(&http.Client{Timeout: 30 * time.Second, Transport: hostTransport})
	client, err := hgapi.NewClient(hgapi.Options{
		Transport:          authenticatedTransport,
		ProfileStore:       profileStore,
		APIOrigin:          "https://" + upstream.DefaultPlaybackHosts[0],
		RetryAttempts:      3,
		PlaybackDeviceFile: filepath.Join(appDir, "playback_device.json"),
	})
	if err != nil {
		log.Fatalf("failed to init Hongguo protocol client: %v", err)
	}
	log.Printf("[startup] stage=protocol-ready elapsedMs=%d", time.Since(started).Milliseconds())

	// 3. Initialize local media streaming & decryption server
	mediaServer, err := media.NewMediaServer(client)
	if err != nil {
		log.Fatalf("failed to init media server: %v", err)
	}
	defer mediaServer.Close()
	log.Printf("[startup] stage=media-ready elapsedMs=%d", time.Since(started).Milliseconds())
	mediaServer.SetEpisodeCacheCallback(store.RememberEpisode)

	if ffmpegPath, err := media.FFmpegCommand(); err != nil {
		log.Printf("[ffmpeg] playback decryption unavailable: %v", err)
	} else {
		log.Printf("[ffmpeg] using %s", ffmpegPath)
	}
	native.SetFeedScrollCapture(false)
	var appRef atomic.Pointer[uiapp.App]
	// 4. Create and bind HongguoService for MyGo IPC
	service := ipc.NewHongguoService(
		client,
		store,
		mediaServer,
		appDir,
		ipc.WithAuthTransport(authenticatedTransport),
		ipc.WithDeviceProfileStore(profileStore),
		ipc.WithSpareDeviceManager(sparedevice.NewManager(store,
			sparedevice.WithClientConstructor(hgapi.NewClient, authenticatedTransport),
			sparedevice.WithLogger(log.Default()),
		)),
		ipc.WithUpstreamTransport(hostTransport),
		ipc.WithHistoryInvalidator(func() {
			if app := appRef.Load(); app != nil {
				app.HistoryDataChanged()
			}
		}),
	)
	defer ipc.CloseService(service)
	mediaServer.SetMultiModelDeviceIdentity(ipc.MediaDeviceIdentityProvider(service))

	if err := playback.MigrateLegacyPreferences(home, filepath.Join(appDir, "native-player.json")); err != nil {
		log.Printf("[native-player] preference migration: %v", err)
	}
	app := uiapp.NewApp(service)
	appRef.Store(app)
	controller := playback.New(service, avkit.NewPlayer, filepath.Join(appDir, "native-player.json"), playback.Hooks{
		Changed: app.PlaybackChanged,
		Progress: func(p playback.Progress) {
			app.UpdateResumeProgress(uiapp.ResumeProgress{SeriesID: p.SeriesID, EpisodeNum: p.EpisodeNum, Position: p.Position, Session: p.Session, Sequence: p.Sequence})
		},
		LibraryChanged: app.LibraryChanged,
		Closed:         app.ClosePlayer,
		Fullscreen:     native.ToggleWindowFullscreen,
		System: func(s playback.State) {
			native.SetFeedScrollCapture(s.Mode != playback.Idle && s.Panel == "")
			active := s.Mode != playback.Idle && s.Playback != nil
			native.SetPlaybackKeepAwake(active && s.Status.Playing && !s.Paused)
			if !active {
				app.SetAmbientGlow(false, [3]uint8{}, [3]uint8{})
				native.ClearNowPlaying()
				return
			}
			native.UpdateNowPlaying(mediaServer.CacheDir(), native.UpdateNowPlayingArgs{Title: s.Book.Title, Series: s.Book.Title, Episode: s.Playback.EpisodeNum, Duration: s.Status.Duration, Position: s.Status.Position, Playing: s.Status.Playing && !s.Paused, Rate: s.Preferences.Rate, CoverURL: s.Book.CoverURL})
		},
	})
	host := uiapp.NewNativeMediaHost(controller)
	app.SetMediaHost(host)
	app.SetArtworkSource(mediaServer.CacheDir(), mediaServer.ImageURL)
	app.LoadPreferences(filepath.Join(appDir, "native-ui.json"))
	app.SetConfirmation(func(message string) bool {
		ok, err := native.ShowConfirmDialog(native.ConfirmDialogArgs{Message: message, ConfirmText: "确认", Destructive: true})
		return err == nil && ok
	})

	// Set the app-wide source before Run so the initial sidebar material is
	// already dark; MyGo applies this setting when the app starts.
	mygo.Theme.SetSource(mygo.ThemeDark)

	// 6. Launch macOS native window via MyGo
	mygo.App.WhenReady(func() {
		log.Printf("[startup] stage=native-ready elapsedMs=%d", time.Since(started).Milliseconds())
		// Initialize macOS native capabilities: Menus, Media Controls & NowPlaying
		native.Init()

		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           mainWindowTitle,
			Width:           1280,
			Height:          820,
			MinWidth:        1000,
			MinHeight:       640,
			TitleBarStyle:   mygo.TitleBarHiddenInset,
			Transparent:     true,
			BackgroundColor: uiapp.WindowBackgroundColor,
			Vibrancy:        mygo.VibrancySidebar,
			Content:         ui.View(app.Render),
		})
		native.SetPictureInPictureHandler(host.PictureInPicture)
		native.SetScrollGestureHandler(func(payload native.ScrollGesturePayload) {
			command := "next"
			if payload.Direction == "up" {
				command = "prev"
			}
			app.MediaCommand(native.MediaCommandPayload{Command: command})
		})
		win.OnEnterFullScreen(func() { app.SetPlayerFullscreen(true) })
		win.OnLeaveFullScreen(func() { app.SetPlayerFullscreen(false) })
		app.SetLibraryGrids(uiapp.NewLibraryGrids())
		app.SetInvalidator(win.Invalidate)
		native.SetMediaCommandHandler(app.MediaCommand)
		native.SetNavigateHandler(func(payload native.NavigatePayload) {
			if payload.SeriesID != "" {
				go app.OpenDetail(payload.SeriesID)
				return
			}
			go app.OpenView(payload.View)
		})
		native.SetWindow(win)
		log.Printf("[startup] stage=native-window-created elapsedMs=%d", time.Since(started).Milliseconds())
		win.Show()
		win.Focus()
		native.StartScrollGestureMonitor()
		go func() {
			app.OpenView(initialView)
			if initialView == "settings" {
				app.SetSettingsLoginOpen(true)
			}
		}()
		win.Invalidate()
		// Keep AppKit running while the actor captures its final native clock.
		// Waiting for it from OnClosed would deadlock the main-thread native bridge.
		var stopping, stopped, quitRequested atomic.Bool
		beginShutdown := func(quit bool) {
			if quit {
				quitRequested.Store(true)
			}
			if !stopping.CompareAndSwap(false, true) {
				return
			}
			app.CloseQRLogin()
			go func() {
				host.Close()
				service.FlushPendingProgress()
				app.FlushPreferences()
				stopped.Store(true)
				mygo.RunOnMain(func() {
					if quitRequested.Load() {
						mygo.App.Quit()
					} else {
						win.Close()
					}
				})
			}()
		}
		win.OnClose(func(e *mygo.CloseEvent) {
			if !stopped.Load() {
				e.PreventDefault()
				beginShutdown(false)
			}
		})
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if e.SessionEnding {
				app.CloseQRLogin()
				controller.FlushSnapshotForSessionEnd()
				app.FlushPreferences()
				return
			}
			if !stopped.Load() {
				e.PreventDefault()
				beginShutdown(true)
			}
		})
		win.OnClosed(native.Cleanup)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatalf("app run error: %v", err)
	}
}
