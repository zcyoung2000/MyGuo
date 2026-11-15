package uiapp

import (
	"testing"
	"time"
)

const visibleFullDescription = "林帆在荒岛上的生存局势再次升级。随着高阶兽人不断出现，隐藏在岛上的秘密实验和神秘势力逐渐显露真容，各方势力展开争夺。杨家遭遇重创，杨宁聪经历巨大变故后走上复仇之路；许知夏失踪也牵出更深层的实验基地秘密。与此同时，林帆不断提升实力、扩充营地力量，却也因此成为幕后势力重点关注的目标，一场围绕荒岛真相与生死存亡的更大危机正式展开。"

func TestDetailDescriptionDisclosureTracksActualWrappingAndResize(t *testing.T) {
	a := NewTestApp()
	a.view = "detail"
	a.detail = Detail{ID: "current", Title: "荒岛剧集", Description: visibleFullDescription}
	tt := scrollInputTester(a, 2000, 1000)
	tt.Frame()
	if tt.HasText("展开简介") {
		t.Fatal("fully visible long description has an expand button")
	}
	tt.SetSize(800, 1000)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("展开简介") {
		t.Fatal("narrow, clipped description has no expand button")
	}
	if err := tt.Click("展开简介"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("收起简介") {
		t.Fatal("expanded description cannot be collapsed")
	}
	if err := tt.Click("收起简介"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	tt.SetSize(2000, 1000)
	tt.Frame()
	tt.Frame()
	if tt.HasText("展开简介") || tt.HasText("收起简介") {
		t.Fatal("disclosure remains after all text fits")
	}
}

func TestDetailDescriptionUsesLinesRatherThanCharacterThreshold(t *testing.T) {
	for _, tc := range []struct {
		text   string
		expand bool
	}{{"第一行\n第二行\n第三行", false}, {"第一行\n第二行\n第三行\n第四行", true}} {
		a := NewTestApp()
		a.view = "detail"
		a.detail = Detail{ID: "current", Title: "简介排版", Description: tc.text}
		tt := scrollInputTester(a, 1600, 1000)
		tt.Frame()
		if tt.HasText("展开简介") != tc.expand {
			t.Fatalf("incorrect disclosure for %q", tc.text)
		}
	}
}
