package uiapp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	posterAspectRatio    float32 = float32(4) / float32(3)
	dramaMinCardWidth    float32 = 160
	dramaCardGap         float32 = 16
	cardGridVerticalGap  float32 = 20
	catalogFooterHeight  float32 = 40
	pageContentTopSpace  float32 = 14
	pageContentBottomPad float32 = 12
)

const (
	newReleaseMaxCardWidth        float32 = 340
	newReleaseSparsePosterWidth   float32 = 340
	newReleaseSparseContentRatio  float32 = 0.60
	newReleaseSparseGroup3Ratio   float32 = 0.85
	newReleaseSparseMaxGroupRatio float32 = 1.02
	newReleaseSparseChromeHeight  float32 = 320
)

func newReleasePosterWidth(height float32) float32 {
	available := max(newReleaseSparsePosterWidth*posterAspectRatio, height-newReleaseSparseChromeHeight)
	return min(newReleaseSparsePosterWidth, available/posterAspectRatio)
}

// hoverBackground always names the idle target first, so a pointer leaving
// interrupts toward the correct color instead of freezing at the hovered one.
func hoverBackground(element ui.Element, idle, hovered ui.Color) ui.Element {
	target := idle
	if element.Hovered() {
		target = hovered
	}
	element.Background(element.AnimateColor("background", target, feedbackAnimationValues))
	pose := ui.Pose{}
	if element.Pressed() {
		pose.Scale = 0.98
	}
	element.AnimatePose(pose, cardSpring)
	return element
}

func stableDramaKey(item CatalogItem, fallback int) any {
	if item.ID != "" || item.EpisodeID != "" {
		return fmt.Sprintf("%s|%s", item.ID, item.EpisodeID)
	}
	return fallback
}

func configureDramaGrid(state *ui.GridState, items []CatalogItem) *ui.GridState {
	if state == nil {
		return nil
	}
	state.Key = func(item int) any { return stableDramaKey(items[item], item) }
	state.Label = func(item int) string { return items[item].Title }
	return state
}

func dramaColumns(width float32) int {
	if width <= 0 {
		return 1
	}
	return max(2, int((width+dramaCardGap)/(dramaMinCardWidth+dramaCardGap)))
}

func dramaCardWidth(width float32) float32 {
	return (width - float32(dramaColumns(width)-1)*dramaCardGap) / float32(dramaColumns(width))
}

func (a *App) gridContentWidth(c *ui.Context) float32 {
	sidebarWidth := a.sidebarWidth
	if sidebarWidth == 0 {
		sidebarWidth = defaultSidebarWidth
	}
	width, _ := c.Size()
	return max(320, width-sidebarWidth-32)
}

// dramaSkeleton mirrors the card grid geometry before any content arrives.
func dramaSkeleton(c *ui.Context, count int, width float32) {
	posterGridSkeleton(c, count, width, nil)
}

func posterGridSkeleton(c *ui.Context, count int, width float32, contentHeight func(cardWidth float32) float32) {
	skeleton := pagePlaceholder(c)
	cardWidth := dramaCardWidth(width)
	cardHeight := cardWidth*posterAspectRatio + catalogTextLineHeight + catalogMetadataLineHeight + catalogRowGap*2
	if contentHeight != nil {
		cardHeight = contentHeight(cardWidth)
	}
	posterHeight := cardWidth * posterAspectRatio
	columns := dramaColumns(width)
	ui.Column(c.Key("drama-skeleton")).Gap(cardGridVerticalGap).Children(func() {
		for offset := 0; offset < count; offset += columns {
			rowIndex := offset
			ui.Row(c.Key(rowIndex)).Gap(dramaCardGap).Children(func() {
				for index := offset; index < min(offset+columns, count); index++ {
					ui.Column(c.Key(index)).Width(cardWidth).Height(cardHeight).Gap(catalogRowGap).Children(func() {
						skeleton.bone(c.Key("poster"), 8).Width(cardWidth).Height(posterHeight).
							AspectRatio(posterAspectRatio).Role(ui.RoleImage).Label("加载海报占位")
						skeleton.text(c.Key("title"), 13, catalogTextLineHeight/13).WidthPercent(92)
						skeleton.text(c.Key("metadata"), 12, catalogMetadataLineHeight/12).WidthPercent(boneWidth(index))
					})
				}
			})
		}
	})
}

func sectionHeader(c *ui.Context, title string) {
	ui.Row(c.Key("section-header:" + title)).AlignItems(ui.Baseline).Gap(10).Children(func() {
		ui.Text(c, title).FontSize(17).Bold().TextColor(c.Theme().Text)
	})
}

func detailProgressPieces(pieces ...string) []string {
	out := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		if piece = strings.TrimSpace(piece); piece != "" && piece != "0" && piece != "0次播放" {
			out = append(out, piece)
		}
	}
	return out
}

func formatCountText(value int64) string {
	switch {
	case value <= 0:
		return ""
	case value >= 100000000:
		return strconv.FormatFloat(float64(value)/100000000, 'f', 1, 64) + "亿"
	case value >= 10000:
		return strconv.FormatFloat(float64(value)/10000, 'f', 1, 64) + "万"
	default:
		return strconv.FormatInt(value, 10)
	}
}
