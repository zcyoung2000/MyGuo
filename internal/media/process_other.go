//go:build !windows

package media

import "os/exec"

func configureMediaProcess(cmd *exec.Cmd) {}
