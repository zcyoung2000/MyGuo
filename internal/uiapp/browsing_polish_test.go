package uiapp

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
)

func TestPageGlassStaysDarkAboveBlackContent(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		theme := hongguoTheme(c)
		c.SetTheme(&theme)
		ui.Box(c).Fill().Background(theme.Background).Children(func() {
			ui.Box(c).Size(300, 40).Radius(20).Material(pageChromeMaterial())
		})
	}, 320, 100)
	tt.SetDark(true)
	tt.Frame()
	value := tt.Image().RGBAAt(150, 20)
	if value.R > 60 || value.G > 60 || value.B > 65 {
		t.Fatalf("page control has a silver/white fill: %+v", value)
	}
}

func TestLongFiltersRevealProgressivelyAndKeepSelectionVisible(t *testing.T) {
	a := NewTestApp()
	options := make([]FilterOption, 30)
	for i := range options {
		options[i] = FilterOption{Label: fmt.Sprintf("条件%d", i), Value: fmt.Sprint(i)}
	}
	a.catalog.Filters = []FilterRow{{Title: "主题", Key: "topic", Options: options}}
	a.catalog.Selection = []int{20}
	a.catalog.Items = []CatalogItem{{ID: "drama", Title: "筛选结果"}}
	tt := ui.NewTester(a.Render, 1280, 820)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	if !tt.HasText("条件20") || tt.HasText("条件29") {
		t.Fatal("selected late option must be visible while unused options stay folded")
	}
	before, _ := tt.Find("筛选结果")
	if err := tt.Click("展开主题筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(70 * time.Millisecond)
	middle, _ := tt.Find("筛选结果")
	tt.Advance(240 * time.Millisecond)
	after, _ := tt.Find("筛选结果")
	if !tt.HasText("条件29") || after.Y <= before.Y || middle.Y >= after.Y {
		t.Fatalf("disclosure did not move results progressively: before=%+v middle=%+v after=%+v", before, middle, after)
	}
	if err := tt.Click("收起主题筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if tt.HasText("条件29") || !tt.HasText("条件20") {
		t.Fatal("folding lost the selected condition")
	}
	if err := tt.Click("收起筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if tt.HasText("主题") {
		t.Fatal("manual collapse was overridden at the top of the page")
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("展开筛选"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("主题") {
		t.Fatal("Reduce Motion prevented disclosure from opening")
	}
}

func TestRefreshAndFailureKeepLoadedBrowsingContent(t *testing.T) {
	for _, view := range []string{"discover", "search", "new", "rank"} {
		t.Run(view, func(t *testing.T) {
			a := NewTestApp()
			a.view = view
			item := CatalogItem{ID: "old", Title: "已经加载的剧集"}
			switch view {
			case "discover":
				a.catalog.Items = []CatalogItem{item}
				a.catalog.Loading = true
			case "search":
				a.search = CatalogPage{Items: []CatalogItem{item}, Loading: true}
			case "new":
				a.releases = ReleaseState{Items: []CatalogItem{item}, Loading: true}
			case "rank":
				a.rank = RankingState{Items: []ipc.RankItemDTO{{SeriesID: item.ID, Title: item.Title}}, Loading: true}
			}
			tt := ui.NewTester(a.Render, 1280, 820)
			if !tt.HasText(item.Title) {
				t.Fatal("refresh replaced loaded content with a skeleton")
			}
			switch view {
			case "discover":
				a.catalog.Loading = false
				a.catalog.Error = "更新失败"
			case "search":
				a.search.Loading = false
				a.search.Error = "更新失败"
			case "new":
				a.releases.Loading = false
				a.releases.Error = "更新失败"
			case "rank":
				a.rank.Loading = false
				a.rank.Error = "更新失败"
			}
			tt.Frame()
			if !tt.HasText(item.Title) || !tt.HasText("更新失败") {
				t.Fatal("failed refresh hid the available results or the retry message")
			}
		})
	}
}

func TestHoverSettlesAndReturnsWithoutRetargetLoop(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() {
			hoverBackground(ui.ButtonBase(c).Size(100, 40).Label("反馈"), ui.RGB(20, 20, 20), ui.RGB(70, 70, 70))
		})
	}, 200, 100)
	tt.Move(20, 20)
	tt.Advance(300 * time.Millisecond)
	if tt.FramePending() || tt.Image().RGBAAt(20, 20).R < 65 {
		t.Fatal("hover kept restarting instead of reaching its target")
	}
	tt.Move(180, 80)
	tt.Advance(300 * time.Millisecond)
	if tt.FramePending() || tt.Image().RGBAAt(20, 20).R > 25 {
		t.Fatal("pointer leaving did not restore the idle state")
	}
}

func TestArtworkArrivalOnlyRepaintsAndKeepsGeometry(t *testing.T) {
	var encoded bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			picture.Set(x, y, color.RGBA{230, 30, 70, 255})
		}
	}
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	bitmap, err := ui.DecodeImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	a := NewTestApp()
	a.artwork = newArtworkLoader("", nil, nil)
	a.artwork.entries["poster"] = artworkEntry{retryAt: time.Now().Add(time.Hour)}
	renders := 0
	tt := ui.NewTester(func(c *ui.Context) { renders++; a.image(c, "poster", 100, 140) }, 200, 200)
	tt.Frame()
	entry := a.artwork.entries["poster"]
	entry.bitmap = bitmap
	a.artwork.entries["poster"] = entry
	tt.Frame()
	before := renders
	start := tt.Image().RGBAAt(50, 50)
	tt.Advance(100 * time.Millisecond)
	middle := tt.Image().RGBAAt(50, 50)
	tt.Advance(300 * time.Millisecond)
	end := tt.Image().RGBAAt(50, 50)
	if renders != before {
		t.Fatalf("artwork fade rebuilt the view %d times", renders-before)
	}
	if start.G <= end.G || middle.G >= start.G || middle.G <= end.G {
		t.Fatalf("image did not fade progressively: start=%+v mid=%+v end=%+v", start, middle, end)
	}
}
