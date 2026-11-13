package uiapp

import (
	"context"
	"errors"
	"myguo/internal/ipc"
	"sync/atomic"
	"testing"
	"time"
)

type supplementLoadingProvider struct {
	*parityProvider
	seasonError     error
	failAdaptation  atomic.Bool
	adaptationCalls atomic.Int32
}

func (p *supplementLoadingProvider) GetSeriesSeasons(ctx context.Context, id string) (*ipc.SeriesCollectionDTO, error) {
	if p.seasonError != nil {
		return nil, p.seasonError
	}
	return p.parityProvider.GetSeriesSeasons(ctx, id)
}
func (p *supplementLoadingProvider) GetSeriesAdaptations(context.Context, string, string, string, string, string) (*ipc.SeriesCollectionDTO, error) {
	p.adaptationCalls.Add(1)
	if p.failAdaptation.Load() {
		return nil, errors.New("temporary adaptation error")
	}
	return &ipc.SeriesCollectionDTO{Title: "同一剧本改编", Items: []ipc.SeriesCard{{ID: "adaptation", Title: "改编剧"}}}, nil
}
func TestDetailAdaptationsAlwaysLeaveInitialLoadingState(t *testing.T) {
	for _, tc := range []struct {
		name        string
		seasons     []ipc.SeriesCard
		seasonError error
	}{
		{name: "no seasons"},
		{name: "with seasons", seasons: []ipc.SeriesCard{{ID: "season-two"}}},
		{name: "seasons failed", seasonError: errors.New("seasons unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &supplementLoadingProvider{parityProvider: &parityProvider{seasons: &ipc.SeriesCollectionDTO{Items: tc.seasons}}, seasonError: tc.seasonError}
			app := NewTestApp()
			app.featuresSource = provider
			app.detail = Detail{ID: "series", Seasons: DetailCollection{Loading: true}, Adaptations: DetailCollection{Loading: true}}
			app.loadSupplement("series", app.generation, false)
			waitFor(time.Second, func() bool { app.mu.Lock(); defer app.mu.Unlock(); return !app.detail.Adaptations.Loading })
			app.mu.Lock()
			defer app.mu.Unlock()
			if app.detail.Adaptations.Loading || len(app.detail.Adaptations.Items) != 1 || provider.adaptationCalls.Load() != 1 {
				t.Fatalf("adaptation never completed: %+v calls=%d", app.detail.Adaptations, provider.adaptationCalls.Load())
			}
		})
	}
}
func TestDetailAdaptationFailureCanRetry(t *testing.T) {
	provider := &supplementLoadingProvider{parityProvider: &parityProvider{}}
	provider.failAdaptation.Store(true)
	app := NewTestApp()
	app.featuresSource = provider
	app.detail = Detail{ID: "series", Adaptations: DetailCollection{Loading: true}}
	app.loadSupplement("series", app.generation, true)
	if app.detail.Adaptations.Loading || app.detail.Adaptations.Error == "" {
		t.Fatalf("failed request stuck loading: %+v", app.detail.Adaptations)
	}
	provider.failAdaptation.Store(false)
	app.RetrySupplement(true)
	if app.detail.Adaptations.Loading || app.detail.Adaptations.Error != "" || len(app.detail.Adaptations.Items) != 1 {
		t.Fatalf("retry failed: %+v", app.detail.Adaptations)
	}
}
