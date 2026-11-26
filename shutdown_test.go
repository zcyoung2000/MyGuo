package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// App.Run must own the main OS thread, so lifecycle probes run as isolated
// subprocesses through TestMain. They never open or replace the installed app.
func TestMain(m *testing.M) {
	if root := os.Getenv("MYGUO_SHUTDOWN_PROBE"); root != "" {
		runShutdownProbe(root)
		return
	}
	os.Exit(m.Run())
}

func runShutdownProbe(root string) {
	if _, err := os.Stat(filepath.Join(root, "drained")); err == nil {
		if err := os.WriteFile(filepath.Join(root, "relaunched"), []byte("yes"), 0600); err != nil {
			panic(err)
		}
		return
	}
	mygo.App.SetName("MyGuo shutdown probe")
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{Title: "Shutdown probe", Hidden: true})
		setupShutdown(win, shutdownTasks{
			closeLogin: func() {},
			drain: func() {
				// Like the real playback actor, cleanup needs the main thread.
				mygo.RunOnMain(func() {
					if err := os.WriteFile(filepath.Join(root, "clock"), []byte("42.5"), 0600); err != nil {
						panic(err)
					}
				})
				if err := os.WriteFile(filepath.Join(root, "drained"), []byte("yes"), 0600); err != nil {
					panic(err)
				}
			},
			flushSession: func() {},
		})
		switch os.Getenv("MYGUO_SHUTDOWN_ACTION") {
		case "relaunch":
			mygo.App.Relaunch()
		case "close":
			win.Close()
		default:
			mygo.App.Quit()
		}
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
}

func TestNativeShutdownPreservesRelaunchAndOrdinaryQuit(t *testing.T) {
	if os.Getenv("MYGUO_NATIVE_LIFECYCLE_TESTS") != "1" {
		t.Skip("set MYGUO_NATIVE_LIFECYCLE_TESTS=1 on a desktop to run native subprocess probes")
	}
	for _, action := range []string{"relaunch", "quit", "close"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0])
			cmd.Env = append(os.Environ(), "MYGUO_SHUTDOWN_PROBE="+root, "MYGUO_SHUTDOWN_ACTION="+action)
			cmd.WaitDelay = time.Second
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", action, err, out)
			}
			if clock, err := os.ReadFile(filepath.Join(root, "clock")); err != nil || string(clock) != "42.5" {
				t.Fatalf("native clock was not saved: %q, %v", clock, err)
			}
			if action == "relaunch" {
				for {
					if _, err := os.Stat(filepath.Join(root, "relaunched")); err == nil {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("the application exited without relaunching")
					case <-time.After(10 * time.Millisecond):
					}
				}
			} else if _, err := os.Stat(filepath.Join(root, "relaunched")); err == nil {
				t.Fatal("ordinary quit unexpectedly relaunched the application")
			}
		})
	}
}
