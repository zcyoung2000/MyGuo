package native

// WaitForShutdown waits on the UI thread while servicing the native main loop.
// Quit and Relaunch listeners use it to drain playback without canceling the
// original request. A plain channel wait would deadlock native player calls.
func WaitForShutdown(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
			shutdownStep()
		}
	}
}
