//go:build !darwin

package media

import "context"

func convertImage(ctx context.Context, source, destination string) error {
	ffmpeg, err := ffmpegCommand()
	if err != nil {
		return err
	}
	return mediaCommand(ctx, ffmpeg, "-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", source, "-frames:v", "1", "-q:v", "3", "-f", "image2", destination).Run()
}
