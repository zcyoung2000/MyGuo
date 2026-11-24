package playerengine

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"
	"myguo/internal/avkit"
)

func New() (avkit.Player, error) {
	return newSystemPlayer(func(url string) (systemVideo, error) {
		p, err := video.OpenURL(url)
		if err != nil {
			return nil, err
		}
		return &windowsVideo{p}, nil
	}, mygo.RunOnMain), nil
}

type windowsVideo struct{ p *ui.VideoPlayer }

func (v *windowsVideo) play()                { v.p.Play() }
func (v *windowsVideo) pause()               { v.p.Pause() }
func (v *windowsVideo) close()               { v.p.Close() }
func (v *windowsVideo) seek(seconds float64) { v.p.Seek(time.Duration(seconds * float64(time.Second))) }
func (v *windowsVideo) configure(rate, volume float64, muted bool) {
	v.p.SetRate(rate).SetVolume(volume).SetMuted(muted)
}
func (v *windowsVideo) player() *ui.VideoPlayer { return v.p }
func (v *windowsVideo) snapshot() avkit.Status {
	w, h := v.p.Size()
	s := avkit.Status{
		Position: v.p.Position().Seconds(), Duration: v.p.Duration().Seconds(),
		Ready: v.p.Ready(), Displayed: v.p.Ready(), Playing: v.p.Playing(),
		Buffering: v.p.Buffering(), Ended: v.p.Ended(), Width: w, Height: h,
		RangesUnsupported: true,
	}
	if err := v.p.Err(); err != nil {
		s.Error = err.Error()
	}
	// The public API exposes no buffered or seekable ranges. Leave them
	// unknown rather than treating the length of a growing stream as cached.
	return s
}
