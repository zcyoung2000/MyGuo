// Promo renders a 4:5 (1080×1350) MyGuo launch video using github.com/egoist/mygo/plugins/render.
//
//	go run ./scripts/promo
//	go run ./scripts/promo -preview
//	go run ./scripts/promo -audio bgm.mp3 -o ~/Desktop/MyGuo/MyGuo-promo.mp4
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/render"
	"github.com/egoist/mygo/ui"
)

const (
	videoWidth  = 1080
	videoHeight = 1350
	videoScale  = 2 // Logical DIP canvas: 540 × 675
	fps         = 30
	totalFrames = 1160 // ~38.7s
)

var (
	bgColor     = ui.Hex("#0b0c10")
	cardBg      = ui.Hex("#14161e")
	crimson     = ui.Hex("#fe2c55")
	coral       = ui.Hex("#ff4d6d")
	warmAmber   = ui.Hex("#fbbf80")
	mintGreen   = ui.Hex("#7be495")
	skyBlue     = ui.Hex("#67b7ff")
	softPurple  = ui.Hex("#c084fc")
	mutedText   = ui.RGBA(255, 255, 255, 0.56)
	subtleText  = ui.RGBA(255, 255, 255, 0.78)
	borderLight = ui.RGBA(255, 255, 255, 0.12)
)

type slideSpec struct {
	Tag      string
	Line1    string
	Line2    string
	Accent   ui.Color
	FileName string
	Image    ui.ImageSource
}

type assets struct {
	Icon   ui.ImageSource
	Slides []slideSpec
	Chime  *render.Sound
	BGM    *render.Sound
}

func loadImage(path string) (ui.ImageSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ui.DecodeImage(data)
}

func loadAssets(assetsDir, iconPath, audioPath string) (*assets, error) {
	icon, err := loadImage(iconPath)
	if err != nil {
		return nil, fmt.Errorf("load icon %s: %w", iconPath, err)
	}

	slides := []slideSpec{
		{
			Tag:      "多维找剧",
			Line1:    "可能是东半球最优雅的",
			Line2:    "Mac 红果客户端",
			Accent:   warmAmber,
			FileName: "Snipaste_2026-11-24_17-58-11.png",
		},
		{
			Tag:      "原生架构",
			Line1:    "100% Go，",
			Line2:    "100% Native",
			Accent:   mintGreen,
			FileName: "Snipaste_2026-11-24_17-58-18.png",
		},
		{
			Tag:      "零浏览器",
			Line1:    "他的背后，",
			Line2:    "没有浏览器",
			Accent:   coral,
			FileName: "Snipaste_2026-11-24_17-58-24.png",
		},
		{
			Tag:      "纯 Go 绘制",
			Line1:    "所有的界面，",
			Line2:    "由 Go 绘制",
			Accent:   skyBlue,
			FileName: "Snipaste_2026-11-24_17-58-52.png",
		},
		{
			Tag:      "追剧预约",
			Line1:    "你的短剧，",
			Line2:    "在 Mac 上原生的播放",
			Accent:   softPurple,
			FileName: "Snipaste_2026-11-24_17-58-32.png",
		},
		{
			Tag:      "原生播放",
			Line1:    "原生的 AVKit，",
			Line2:    "更不烧心",
			Accent:   crimson,
			FileName: "Snipaste_2026-11-24_17-57-34.png",
		},
	}

	for i := range slides {
		imgPath := filepath.Join(assetsDir, slides[i].FileName)
		img, err := loadImage(imgPath)
		if err != nil {
			return nil, fmt.Errorf("load slide %d (%s): %w", i+1, imgPath, err)
		}
		slides[i].Image = img
	}

	var bgm *render.Sound
	if audioPath != "" {
		bgm, err = openAudioAnyFormat(audioPath)
		if err != nil {
			return nil, fmt.Errorf("load audio %s: %w", audioPath, err)
		}
	}

	return &assets{
		Icon:   icon,
		Slides: slides,
		Chime:  newSoftChime(),
		BGM:    bgm,
	}, nil
}

func openAudioAnyFormat(path string) (*render.Sound, error) {
	if strings.EqualFold(filepath.Ext(path), ".wav") {
		return render.OpenSound(path)
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path, "-vn", "-f", "wav", "-acodec", "pcm_s16le", "-ar", "48000", "-ac", "2", "pipe:1")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decode %s: %w", path, err)
	}
	return render.DecodeSound(out)
}

// newSoftChime synthesizes a gentle, subtle low-gain harmonic pluck for scene transitions.
func newSoftChime() *render.Sound {
	const rate = 48000
	pcm := make([]float32, rate/2)
	for i := range pcm {
		t := float64(i) / rate
		env := math.Exp(-8.5*t) * min(t/0.008, 1)
		pcm[i] = float32(0.14 * env * (math.Sin(2*math.Pi*523.25*t) + 0.35*math.Sin(2*math.Pi*1046.5*t)))
	}
	return render.SoundOf(rate, 1, pcm)
}

func promoComposition(a *assets, blurSamples int) render.Composition {
	return render.Composition{
		Width:      videoWidth,
		Height:     videoHeight,
		FPS:        fps,
		Frames:     totalFrames,
		Scale:      videoScale,
		Dark:       true,
		Background: bgColor,
		MotionBlur: render.MotionBlur{Samples: blurSamples, Shutter: 180},
		View: func(c *ui.Context, f render.Frame) {
			if a.BGM != nil {
				vol := f.Interpolate(
					[]float64{0, 18, float64(totalFrames - 45), float64(totalFrames - 1)},
					[]float64{0, 0.55, 0.55, 0},
				)
				f.Audio(a.BGM, render.AudioOptions{Volume: vol, Loop: true})
			}

			renderBackdrop(c, f)

			// Intro 1: 可能是东半球最优雅的 Mac 红果客户端
			f.Sequence(c, 0, 78, func(sf render.Frame) {
				renderIntroText(c, sf, a, "可能是东半球最优雅的", "Mac 红果客户端", warmAmber)
			})
			// Intro 2: 100% Go，100% Native / 他的背后，没有浏览器
			f.Sequence(c, 68, 82, func(sf render.Frame) {
				renderIntroText(c, sf, a, "100% Go，100% Native", "他的背后，没有浏览器", coral)
			})
			// Brand Reveal: App Icon + MyGuo + 现已发布 + 所有的界面，由 Go 绘制
			f.Sequence(c, 140, 88, func(sf render.Frame) {
				renderBrandReveal(c, sf, a)
			})

			// Persistent top header across the 6 showcase slides + recap
			f.Sequence(c, 224, 832, func(sf render.Frame) {
				renderShowcaseChrome(c, sf, a)
			})

			// 6 showcase cards (128f each, 12f overlap)
			const slideStart = 224
			const slideStep = 116
			const slideDur = 128
			for i, slide := range a.Slides {
				idx := i
				sl := slide
				start := slideStart + idx*slideStep
				f.Sequence(c, start, slideDur, func(sf render.Frame) {
					renderSlide(c, sf, a, idx, len(a.Slides), sl)
				})
			}

			// Recap checklist card (all 6 core lines)
			recapStart := slideStart + len(a.Slides)*slideStep - 8
			f.Sequence(c, recapStart, 130, func(sf render.Frame) {
				renderRecapCard(c, sf, a)
			})

			// Outro: Icon + MyGuo + 原生的 AVKit，更不烧心 + GitHub pill
			outroStart := recapStart + 116
			f.Sequence(c, outroStart, 0, func(sf render.Frame) {
				renderOutro(c, sf, a)
			})
		},
	}
}

func renderBackdrop(c *ui.Context, f render.Frame) {
	render.Layer(c).Children(func() {
		pulse := float32(1 + 0.012*math.Sin(f.Seconds()*0.8))
		ui.Box(c.Key("glow-top")).Absolute().Top(-80).Left(40).Size(460, 460).
			RadialGradient(ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.45},
				Radius: 230,
				Stops: []ui.GradientStop{
					{Color: ui.RGBA(254, 44, 85, 0.10), At: 0},
					{Color: ui.RGBA(254, 44, 85, 0.02), At: 0.55},
					{Color: ui.Transparent, At: 1},
				},
			})

		for i := 0; i < 6; i++ {
			size := float32(220+i*72) * pulse
			offset := -size * 0.42
			ui.Box(c.Key(fmt.Sprintf("ring-%d", i))).Absolute().
				Left(offset).Bottom(offset).Size(size, size).
				Radius(size/2).
				Border(1, ui.RGBA(255, 255, 255, 0.028))
		}
	})
}

func renderIntroText(c *ui.Context, f render.Frame, a *assets, line1, line2 string, accent ui.Color) {
	if a.BGM == nil {
		f.Audio(a.Chime, render.AudioOptions{Volume: 0.45})
	}
	in1 := f.Spring(ui.Snappy.Spring)
	f2, _ := f.Shift(7, 0)
	in2 := f2.Spring(ui.Snappy.Spring)

	out := f.Interpolate([]float64{float64(f.Count - 14), float64(f.Count - 1)}, []float64{1, 0})
	exitY := float32(f.Interpolate([]float64{float64(f.Count - 16), float64(f.Count - 1)}, []float64{0, -18}, render.Interpolation{Easing: ui.EaseIn}))

	// Subtle minimal crimson hairline indicator instead of a music waveform
	lineW := float32(f.Interpolate([]float64{6, 26}, []float64{0, 64}, render.Interpolation{Easing: ui.EaseOut}))

	ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Padding(0, 32).Gap(28).Opacity(float32(out)).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(10).Translate(0, exitY).Children(func() {
			ui.Text(c, line1).FontSize(34).FontWeight(800).TextColor(ui.Hex("#ffffff")).TextAlign(ui.Center).
				Opacity(float32(min(in1*2, 1))).
				Translate(0, float32(20*(1-in1)))
			ui.Text(c, line2).FontSize(38).FontWeight(900).TextColor(accent).TextAlign(ui.Center).
				Opacity(float32(min(in2*2, 1))).
				Translate(0, float32(22*(1-in2)))
		})

		ui.Box(c).Width(lineW).Height(3).Radius(2).Background(accent).Opacity(0.85)
	})
}

func renderBrandReveal(c *ui.Context, f render.Frame, a *assets) {
	if a.BGM == nil {
		f.Audio(a.Chime, render.AudioOptions{Volume: 0.55})
	}
	pop := f.Spring(ui.Bouncy.Spring)
	tagFrame, _ := f.Shift(12, 0)
	tagIn := tagFrame.Spring(ui.Snappy.Spring)
	out := f.Interpolate([]float64{float64(f.Count - 14), float64(f.Count - 1)}, []float64{1, 0})
	reveal := f.Interpolate([]float64{6, 24}, []float64{0, 1}, render.Interpolation{Easing: ui.EaseOut})

	ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(20).Opacity(float32(out)).Children(func() {
		scale := float32(0.75 + 0.25*pop)
		ui.Box(c).Size(134, 134).Radius(30).Clip().
			Shadow(0, 18, 44, 0, ui.RGBA(0, 0, 0, 0.65)).
			Scale(scale, scale).Opacity(float32(min(pop*1.8, 1))).Children(func() {
			ui.Image(c, a.Icon).Fill().Fit(ui.Cover)
		})

		ui.Column(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, "MyGuo").FontSize(48).FontWeight(900).LetterSpacing(-1).TextColor(ui.Hex("#ffffff")).
				Opacity(float32(reveal)).Translate(0, float32((1-reveal)*14))

			ui.Row(c).Gap(10).AlignItems(ui.Center).Opacity(float32(min(tagIn*2, 1))).Translate(0, float32(14*(1-tagIn))).Children(func() {
				ui.Box(c).Padding(4, 14).Radius(999).Background(crimson).Children(func() {
					ui.Text(c, "现已发布").FontSize(13).Bold().TextColor(ui.Hex("#ffffff"))
				})
				ui.Text(c, "v0.1.0").FontSize(14).TextColor(subtleText)
			})

			ui.Text(c, "所有的界面，由 Go 绘制").FontSize(18).TextColor(mutedText).
				Opacity(float32(tagFrame.Interpolate([]float64{4, 20}, []float64{0, 1})))
		})
	})
}

func renderShowcaseChrome(c *ui.Context, f render.Frame, a *assets) {
	in := f.Interpolate([]float64{0, 14}, []float64{0, 1})
	out := f.Interpolate([]float64{float64(f.Count - 16), float64(f.Count - 1)}, []float64{1, 0})
	active := min(max(f.Index/116, 0), len(a.Slides)-1)

	ui.Row(c).Absolute().Top(22).Left(26).Right(26).AlignItems(ui.Center).
		Opacity(float32(min(in, out))).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(24, 24).Radius(6).Clip().Children(func() {
				ui.Image(c, a.Icon).Fill().Fit(ui.Cover)
			})
			ui.Text(c, "MyGuo").FontSize(15).FontWeight(700).TextColor(ui.Hex("#ffffff"))
		})
		ui.Spacer(c)
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			for i := 0; i < len(a.Slides); i++ {
				dotColor := ui.RGBA(255, 255, 255, 0.22)
				dotW := float32(8)
				if i <= active {
					dotColor = crimson
				}
				if i == active {
					dotW = 18
				}
				ui.Box(c.Key(fmt.Sprintf("dot-%d", i))).Width(dotW).Height(8).Radius(4).Background(dotColor)
			}
		})
	})
}

func renderSlide(c *ui.Context, f render.Frame, a *assets, index, total int, slide slideSpec) {
	if a.BGM == nil {
		f.Audio(a.Chime, render.AudioOptions{Volume: 0.35})
	}
	in := f.Spring(ui.Snappy.Spring)
	out := f.Interpolate([]float64{float64(f.Count - 14), float64(f.Count - 1)}, []float64{1, 0})
	exitX := float32(f.Interpolate([]float64{float64(f.Count - 16), float64(f.Count - 1)}, []float64{0, -40}, render.Interpolation{Easing: ui.EaseIn}))

	cardFrame, _ := f.Shift(4, 0)
	cardIn := cardFrame.Spring(ui.Snappy.Spring)
	kenBurns := float32(f.Interpolate([]float64{0, float64(f.Count - 1)}, []float64{1.0, 1.03}))

	ui.Column(c).Fill().Padding(76, 24, 24, 24).Gap(22).Opacity(float32(min(in*2, out))).Children(func() {
		ui.Column(c).Gap(10).Translate(exitX, float32(16*(1-in))).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%02d / %02d", index+1, total)).FontSize(13).Bold().LetterSpacing(1.5).TextColor(slide.Accent)
				ui.Text(c, "——  "+slide.Tag).FontSize(13).TextColor(mutedText)
			})
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, slide.Line1).FontSize(34).FontWeight(900).TextColor(ui.Hex("#ffffff"))
				ui.Text(c, slide.Line2).FontSize(36).FontWeight(900).TextColor(slide.Accent)
			})
		})

		ui.Box(c).FillWidth().Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Box(c).Width(492).Height(315).Radius(16).Clip().
				Background(cardBg).
				Border(1, borderLight).
				Shadow(0, 22, 54, 0, ui.RGBA(0, 0, 0, 0.72)).
				Translate(exitX, float32(28*(1-cardIn))).
				Scale(kenBurns*float32(0.96+0.04*cardIn), kenBurns*float32(0.96+0.04*cardIn)).
				Children(func() {
					ui.Image(c, slide.Image).Fill().Fit(ui.Cover)
				})
		})
	})
}

func renderRecapCard(c *ui.Context, f render.Frame, a *assets) {
	if a.BGM == nil {
		f.Audio(a.Chime, render.AudioOptions{Volume: 0.45})
	}
	rise := f.Spring(ui.Snappy.Spring)
	out := f.Interpolate([]float64{float64(f.Count - 16), float64(f.Count - 1)}, []float64{1, 0})

	items := []string{
		"可能是东半球最优雅的 Mac 红果客户端",
		"100% Go，100% Native",
		"他的背后，没有浏览器",
		"所有的界面，由 Go 绘制",
		"你的短剧，在 Mac 上原生的播放",
		"原生的 AVKit，更不烧心",
	}

	ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Padding(60, 24, 24, 24).
		Opacity(float32(min(rise*2, out))).Children(func() {
		ui.Column(c).Width(460).Padding(24).Gap(16).Radius(22).
			Background(cardBg).Border(1, borderLight).
			Shadow(0, 24, 60, 0, ui.RGBA(0, 0, 0, 0.7)).
			Translate(0, float32(34*(1-rise))).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(28, 28).Radius(7).Clip().Children(func() {
					ui.Image(c, a.Icon).Fill().Fit(ui.Cover)
				})
				ui.Text(c, "MyGuo").FontSize(18).FontWeight(800).TextColor(ui.Hex("#ffffff"))
				ui.Spacer(c)
				ui.Text(c, "6 / 6").FontSize(13).TextColor(mutedText)
			})

			ui.Column(c).Gap(10).Children(func() {
				for i, line := range items {
					rf, _ := f.Shift(8 + i*6, 0)
					s := rf.Spring(ui.Snappy.Spring)
					rowBg := ui.RGBA(255, 255, 255, 0.03)
					if i == len(items)-1 {
						rowBg = ui.RGBA(254, 44, 85, 0.14)
					}
					ui.Row(c.Key(fmt.Sprintf("recap-%d", i))).Padding(10, 14).Radius(12).
						Background(rowBg).AlignItems(ui.Center).Gap(12).
						Opacity(float32(min(s*2, 1))).Translate(0, float32(14*(1-s))).Children(func() {
						ui.Text(c, fmt.Sprintf("%02d", i+1)).FontSize(12).Bold().TextColor(crimson)
						ui.Text(c, line).FontSize(15).FontWeight(700).TextColor(ui.Hex("#ffffff")).Grow(1)
						ui.Box(c).Size(8, 8).Radius(4).Background(crimson)
					})
				}
			})
		})
	})
}

func renderOutro(c *ui.Context, f render.Frame, a *assets) {
	if a.BGM == nil {
		f.Audio(a.Chime, render.AudioOptions{Volume: 0.55})
	}
	pop := f.Spring(ui.Bouncy.Spring)
	tf, _ := f.Shift(10, 0)
	textIn := tf.Spring(ui.Snappy.Spring)

	ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(20).Children(func() {
		scale := float32(0.75 + 0.25*pop)
		ui.Box(c).Size(128, 128).Radius(28).Clip().
			Shadow(0, 18, 44, 0, ui.RGBA(0, 0, 0, 0.65)).
			Scale(scale, scale).Opacity(float32(min(pop*1.8, 1))).Children(func() {
			ui.Image(c, a.Icon).Fill().Fit(ui.Cover)
		})

		ui.Column(c).AlignItems(ui.Center).Gap(10).
			Opacity(float32(min(textIn*2, 1))).Translate(0, float32(18*(1-textIn))).Children(func() {
			ui.Text(c, "MyGuo").FontSize(46).FontWeight(900).LetterSpacing(-1).TextColor(ui.Hex("#ffffff"))
			ui.Text(c, "可能是东半球最优雅的 Mac 红果客户端").FontSize(20).TextColor(subtleText)
			ui.Text(c, "原生的 AVKit，更不烧心").FontSize(18).TextColor(warmAmber)
		})

		pf, _ := f.Shift(18, 0)
		pillIn := pf.Spring(ui.Snappy.Spring)
		ui.Box(c).Margin(6, 0, 0, 0).Padding(10, 24).Radius(999).Background(crimson).
			Shadow(0, 8, 24, 0, ui.RGBA(254, 44, 85, 0.35)).
			Opacity(float32(min(pillIn*2, 1))).Translate(0, float32(14*(1-pillIn))).Children(func() {
			ui.Text(c, "github.com/zcyoung2000/MyGuo").FontSize(15).Bold().TextColor(ui.Hex("#ffffff"))
		})
	})
}

func main() {
	home, _ := os.UserHomeDir()
	defaultAssets := filepath.Join(home, "Desktop", "MyGuo")

	defaultAudio := filepath.Join("tmp_promo", "bgm", "Chill_Wave.mp3")
	if _, err := os.Stat(defaultAudio); err != nil {
		defaultAudio = ""
	}

	assetsDir := flag.String("assets", defaultAssets, "directory containing the MyGuo PNG screenshots")
	iconPath := flag.String("icon", filepath.Join("resources", "icon.png"), "path to MyGuo app icon PNG")
	audioPath := flag.String("audio", defaultAudio, "optional BGM audio path (mp3/wav/aac/flac/ogg)")
	outPath := flag.String("o", filepath.Join("tmp_promo", "MyGuo-promo.mp4"), "output video (.mp4, .mov, .webm) or image pattern (frames/%04d.png)")
	codec := flag.String("codec", "h264", "h264, hevc, prores, prores4444 or vp9")
	renderer := flag.String("renderer", "auto", "auto, gpu or cpu")
	samples := flag.Int("samples", 5, "motion blur samples (1 disables blur)")
	preview := flag.Bool("preview", false, "preview the composition in a native MyGo window")
	useFFmpeg := flag.Bool("ffmpeg", false, "encode using ffmpeg instead of system encoder")
	flag.Parse()

	loaded, err := loadAssets(*assetsDir, *iconPath, *audioPath)
	if err != nil {
		log.Fatal(err)
	}

	comp := promoComposition(loaded, *samples)
	if *preview {
		previewWindow(comp)
		return
	}

	if err := os.MkdirAll(filepath.Dir(*outPath), 0755); err != nil {
		log.Fatal(err)
	}

	opts := render.Options{
		Renderer: map[string]render.Renderer{"auto": render.Auto, "gpu": render.GPU, "cpu": render.CPU}[*renderer],
		Codec: map[string]render.Codec{
			"h264":       render.H264,
			"hevc":       render.HEVC,
			"prores":     render.ProRes,
			"prores4444": render.ProRes4444,
			"vp9":        render.VP9,
		}[*codec],
		Progress: func(p render.Progress) {
			if p.Done%fps == 0 || p.Done == p.Frames {
				fmt.Printf("\r%s: %d/%d frames (%.1f fps)", p.Renderer, p.Done, p.Frames, float64(p.Done)/max(p.Elapsed.Seconds(), 0.001))
			}
		},
	}
	if *useFFmpeg {
		opts.Encoder = render.FFmpeg
	}

	start := time.Now()
	var renderErr error
	if strings.Contains(*outPath, "%") {
		renderErr = render.Images(context.Background(), comp, *outPath, opts)
	} else {
		renderErr = render.Video(context.Background(), comp, *outPath, opts)
	}
	fmt.Println()
	if renderErr != nil {
		log.Fatal(renderErr)
	}
	abs, _ := filepath.Abs(*outPath)
	fmt.Printf("Rendered %s in %v\n", abs, time.Since(start).Round(time.Millisecond))
}

func previewWindow(comp render.Composition) {
	player, err := render.NewPlayer(comp)
	if err != nil {
		log.Fatal(err)
	}
	player.SetLoop(true)
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title: "MyGuo Promo Preview", Width: 540, Height: 740,
			Content: ui.View(func(c *ui.Context) { previewView(c, player) }),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}

func previewView(c *ui.Context, p *ui.VideoPlayer) {
	ui.Column(c).Fill().Padding(14).Gap(10).Background(bgColor).Children(func() {
		ui.Video(c, p).Grow(1).FillWidth()
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			label := "暂停"
			if !p.Playing() {
				label = "播放"
			}
			if ui.Button(c, label).Clicked() {
				if p.Playing() {
					p.Pause()
				} else {
					p.Play()
				}
			}
			at := p.Position().Seconds()
			if ui.Slider(c, &at, 0, p.Duration().Seconds()).Grow(1).Changed() {
				p.Seek(time.Duration(at * float64(time.Second)))
			}
			ui.Text(c, fmt.Sprintf("%04d/%d", int(p.Position().Seconds()*fps), totalFrames)).TextColor(mutedText).Width(84)
		})
	})
}
