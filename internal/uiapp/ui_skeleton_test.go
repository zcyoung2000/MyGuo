package uiapp

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

const loadingLayoutSettle = 350 * time.Millisecond

func assertQuietLoading(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for _, text := range tt.Texts() {
		for _, hint := range []string{"正在加载", "加载中", "正在搜索", "正在更新", "滑动到底", "加载更多"} {
			if strings.Contains(text, hint) {
				t.Fatalf("visible loading hint %q", text)
			}
		}
	}
}

func TestInitialPlaceholdersStayStillAndContentSettles(t *testing.T) {
	app := NewTestApp()
	app.catalog.Loading = true
	tt := ui.NewTester(app.Render, 1000, 800)
	tt.SetFocused(false)
	tt.Advance(loadingLayoutSettle)
	tt.Frame()
	assertQuietLoading(t, tt)
	before := append([]byte(nil), tt.Image().Pix...)
	tt.Advance(3 * time.Second)
	tt.Frame()
	if !bytes.Equal(before, tt.Image().Pix) || tt.FramePending() {
		t.Fatal("loading placeholders animate or keep requesting frames")
	}
	app.catalog.Loading = false
	app.catalog.Items = []CatalogItem{{ID: "123456", Title: "加载完成样本"}}
	tt.Frame()
	tt.Advance(loadingLayoutSettle)
	tt.Frame()
	if !tt.HasText("加载完成样本") || tt.FramePending() {
		t.Fatal("quiet loading did not settle into content")
	}
}

func TestBrowsingLoadingHasNoPromptsOrLoops(t *testing.T) {
	for _, view := range []string{"discover", "search", "history", "rank", "new", "detail", "actor"} {
		t.Run(view, func(t *testing.T) {
			a := NewTestApp()
			a.view = view
			a.catalog.Loading = true
			a.search.Loading = true
			a.library.History.Loading = true
			a.rank.Loading = true
			a.releases.Loading = true
			a.detail.Loading = true
			a.actor.Loading = true
			tt := ui.NewTester(a.Render, 1000, 800)
			tt.SetFocused(false)
			tt.Advance(loadingLayoutSettle)
			tt.Frame()
			assertQuietLoading(t, tt)
			tt.Advance(3 * time.Second)
			tt.Frame()
			if tt.FramePending() {
				t.Fatal("quiet loading kept scheduling animation frames")
			}
		})
	}
}

func TestHoverFeedbackCoversClickableNativeSurfaces(t *testing.T) {
	source := readFile(t, "view.go") + readFile(t, "feature_views.go") + readFile(t, "feed_chrome.go")
	for _, pattern := range []string{
		`hoverBackground[(]row,`,
		`hoverBackground[(]chip,`,
		`hoverBackground[(]card,`,
	} {
		if !containsRegexp(source, pattern) {
			t.Fatalf("hover feedback pattern missing: %s", pattern)
		}
	}
	if containsRegexp(source, `Transition[(]ui[.]ElementTransition[{]Duration: 150 [*] time[.]Millisecond`) {
		t.Fatal("legacy inline hover transition found")
	}
}

func TestSkeletonGeometryMatchesReadyContent(t *testing.T) {
	app := NewTestApp()
	app.view = "search"
	app.search.Loading = true
	tester := ui.NewTester(app.Render, 1600, 1000)
	tester.Frame()
	loadingBar, loadingBarOK := tester.Find("搜索短剧")
	loadingHeader, loadingHeaderOK := tester.Find("返回上一页")
	skeleton, skeletonOK := tester.AccessByRole(ui.RoleImage, "加载海报占位")
	if !skeletonOK {
		t.Fatal("search skeleton bone missing")
	}
	if !loadingBarOK || !loadingHeaderOK {
		t.Fatalf("search loading page missing: %v", tester.Texts())
	}
	if loadingHeader.Y > loadingBar.Y {
		t.Fatalf("search header is not rendered before the search bar: header=%+v bar=%+v", loadingHeader, loadingBar)
	}
	app.search.Loading = false
	app.search.Items = []CatalogItem{{ID: "123456", EpisodeID: "654321", Title: "几何稳定样本"}}
	tester.Frame()
	content, contentOK := tester.Find("几何稳定样本")
	readyBar, readyBarOK := tester.Find("搜索短剧")
	if !contentOK || !readyBarOK {
		t.Fatal("search ready content missing")
	}
	if math.Abs(float64(skeleton.Bounds.X-content.X)) > 2 {
		t.Fatalf("skeleton-to-content left edge shifted: skeleton=%+v content=%+v", skeleton.Bounds, content)
	}
	widthTolerance := max(2, content.W*0.03)
	if math.Abs(float64(loadingBar.X-readyBar.X)) > 2 || math.Abs(float64(loadingBar.Y-readyBar.Y)) > 2 {
		t.Fatalf("search bar shifted between loading and ready: loading=%+v ready=%+v", loadingBar, readyBar)
	}
	if math.Abs(float64(skeleton.Bounds.W-content.W)) > float64(widthTolerance) {
		t.Fatalf("skeleton bone does not use the visible grid width: bone=%+.2f card=%+.2f", skeleton.Bounds.W, content.W)
	}
}

func TestDiscoverAndNewSkeletonsMatchTheirReadyShapes(t *testing.T) {
	width, height := 1600, 1000

	discoverLoading := NewTestApp()
	discoverLoading.view = "discover"
	discoverLoading.catalog.Loading = true
	discoverTester := ui.NewTester(discoverLoading.Render, width, height)
	discoverTester.Frame()
	discoverBone, discoverBoneOK := discoverTester.AccessByRole(ui.RoleImage, "加载海报占位")
	if !discoverBoneOK {
		t.Fatal("discover skeleton poster missing")
	}
	discoverLoading.catalog.Loading = false
	discoverLoading.catalog.Items = []CatalogItem{{ID: "discover-skeleton", Title: "找剧真实卡片"}}
	discoverTester.Frame()
	discoverReady, discoverReadyOK := discoverTester.Find("找剧真实卡片")
	if !discoverReadyOK {
		t.Fatal("discover ready card missing")
	}
	if absDiff(float64(discoverBone.Bounds.X), float64(discoverReady.X)) > 2 ||
		absDiff(float64(discoverBone.Bounds.W), float64(discoverReady.W)) > 2 ||
		absDiff(float64(discoverBone.Bounds.H), float64(discoverReady.W*posterAspectRatio)) > 2 {
		t.Fatalf("discover skeleton does not preserve the poster row geometry: bone=%+v card=%+v", discoverBone.Bounds, discoverReady)
	}

	newLoading := NewTestApp()
	newLoading.view = "new"
	newLoading.releases.Loading = true
	newLoading.releases.Calendar = true
	newLoading.releases.Dates = []string{"20261106", "20261107"}
	newTester := ui.NewTester(newLoading.Render, width, height)
	newTester.Frame()
	var newBones []ui.AccessInfo
	for _, access := range newTester.AccessAll() {
		if access.Role == ui.RoleImage && access.Name == "加载海报占位" {
			newBones = append(newBones, access)
		}
	}
	if len(newBones) != 2 {
		t.Fatalf("calendar skeleton does not mirror the number of results: %d", len(newBones))
	}
	newLoading.releases.Loading = false
	newLoading.releases.Items = []CatalogItem{
		{ID: "new-one", Title: "新剧真实卡片一", Upcoming: true, PublishAt: "2026-11-06"},
		{ID: "new-two", Title: "新剧真实卡片二", Upcoming: true, PublishAt: "2026-11-07"},
	}
	newTester.Frame()
	newReady, newReadyOK := newTester.AccessByRole(ui.RoleImage, "poster:new-one|")
	if !newReadyOK {
		t.Fatal("calendar ready poster missing")
	}
	if absDiff(float64(newBones[0].Bounds.X), float64(newReady.Bounds.X)) > 2 ||
		absDiff(float64(newBones[0].Bounds.W), float64(newReady.Bounds.W)) > 2 {
		t.Fatalf("calendar skeleton does not preserve the feature row geometry: bone=%+v title=%+v", newBones[0].Bounds, newReady)
	}
}
