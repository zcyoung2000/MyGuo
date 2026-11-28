package media

import (
	"context"
	"os/exec"
	"time"
)

func mediaCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	configureMediaProcess(cmd)
	return cmd
}
