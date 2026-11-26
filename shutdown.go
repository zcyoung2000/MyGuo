package main

import (
	"sync/atomic"

	"github.com/egoist/mygo"
	"myguo/internal/native"
)

type shutdownTasks struct {
	closeLogin   func()
	drain        func()
	flushSession func()
}

func setupShutdown(win *mygo.Window, tasks shutdownTasks) {
	var stopping, stopped, quitRequested atomic.Bool
	done := make(chan struct{})
	begin := func(quit bool) {
		if quit {
			quitRequested.Store(true)
		}
		if !stopping.CompareAndSwap(false, true) {
			return
		}
		tasks.closeLogin()
		go func() {
			tasks.drain()
			stopped.Store(true)
			close(done)
			mygo.RunOnMain(func() {
				if !quitRequested.Load() {
					win.Close()
				}
			})
		}()
	}
	win.OnClose(func(e *mygo.CloseEvent) {
		if !stopped.Load() {
			e.PreventDefault()
			begin(false)
		}
	})
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if e.SessionEnding {
			tasks.closeLogin()
			tasks.flushSession()
			return
		}
		if !stopped.Load() {
			begin(true)
			// Let the original Quit/Relaunch finish. PreventDefault would erase
			// MyGo's relaunch intent. Servicing native tasks here also lets the
			// playback actor read its final clock without blocking the UI thread.
			native.WaitForShutdown(done)
		}
	})
}
