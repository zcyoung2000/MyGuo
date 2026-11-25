package main

import (
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	updatenative "github.com/egoist/mygo/plugins/updater/native"
	"myguo/internal/uiapp"
)

func setupUpdates(app *uiapp.App) {
	mygo.Use(updatenative.New(updater.Options{Language: "zh-Hans"}))
	mygo.App.WhenReady(func() {
		var installed atomic.Bool
		refresh := func() {
			app.SetSoftwareUpdateState(uiapp.SoftwareUpdateState{
				Version:            mygo.App.Version(),
				Enabled:            mygo.Updater.Enabled(),
				AutomaticChecks:    updater.AutomaticChecks(),
				AutomaticDownloads: updater.AutomaticDownloads(),
				LastCheck:          updater.LastCheck(),
				AvailableVersion:   updater.AvailableVersion(),
				Installed:          installed.Load(),
			})
		}
		app.SetSoftwareUpdateActions(uiapp.SoftwareUpdateActions{
			Check:                 updater.CheckForUpdates,
			SetAutomaticChecks:    updater.SetAutomaticChecks,
			SetAutomaticDownloads: updater.SetAutomaticDownloads,
			Restart:               func() { mygo.App.Relaunch() },
		})
		updater.OnChange(refresh)
		updater.OnInstalled(func() {
			installed.Store(true)
			refresh()
		})
		refresh()
	})
}
