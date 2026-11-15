package uiapp

import "github.com/egoist/mygo/ui"

const (
	detailDescriptionFontSize float32 = 13
	detailDescriptionLines            = 3
)

type descriptionLayout struct {
	text    string
	width   float32
	font    ui.Font
	clipped bool
}

// Text has a line limit but no truncation query (MyGo v0.9.1). ShapeText
// uses the same system fonts and wrapping as Text. Cache its line count until
// the content, available width or font changes; no character-count estimate.
func (a *App) descriptionClipped(c *ui.Context, text string, width float32) bool {
	if width <= 0 {
		return false
	}
	font := ui.Font{Family: c.Theme().Font, Size: detailDescriptionFontSize, Lang: c.Locale()}
	cached := &a.detailDescriptionLayout
	if cached.text != text || cached.width != width || cached.font != font {
		*cached = descriptionLayout{text: text, width: width, font: font, clipped: len(ui.ShapeText(text, font, width).Lines()) > detailDescriptionLines}
	}
	return cached.clipped
}

func (a *App) renderDetailDescription(c *ui.Context, text string, width float32) {
	clipped := a.descriptionClipped(c, text, width)
	ui.Column(c.Key("detail-description-block")).FillWidth().MinWidth(0).Gap(4).Transition(sectionTransition).Children(func() {
		desc := ui.Text(c.Key("detail-description"), text).FillWidth().FontSize(detailDescriptionFontSize).TextColor(ui.RGBA(255, 255, 255, .76))
		if !a.detailDescriptionOpen {
			desc.MaxLines(detailDescriptionLines)
		}
		if clipped {
			label := "展开简介"
			if a.detailDescriptionOpen {
				label = "收起简介"
			}
			ui.Button(c, label).FontSize(11).Padding(3, 0).AlignSelf(ui.Start).OnClick(func() { a.detailDescriptionOpen = !a.detailDescriptionOpen; c.Invalidate() })
		}
	})
}
