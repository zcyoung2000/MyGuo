package media

import "context"

func convertImage(ctx context.Context, source, destination string) error {
	return mediaCommand(ctx, "/usr/bin/sips", "-s", "format", "jpeg", source, "--out", destination).Run()
}
