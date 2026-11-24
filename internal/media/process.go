package media

import (
	"context"
	"os/exec"
)

func mediaCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureMediaProcess(cmd)
	return cmd
}
