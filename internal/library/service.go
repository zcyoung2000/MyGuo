package library

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"myguo/internal/hgapi"
	"myguo/internal/hgapi/model"
	"myguo/internal/lifecycle"
	userstore "myguo/internal/store"
)

type (
	HistoryItem     = userstore.HistoryItem
	FavoriteItem    = userstore.FavoriteItem
	LikeItem        = userstore.LikeItem
	ReservationItem = userstore.ReservationItem
)

type Notification struct {
	Title    string
	Body     string
	SeriesID string
}

const remoteSnapshotSettleDelay = 5 * time.Second

type Dependencies struct {
	Store             *userstore.Store
	Client            *hgapi.Client
	ImageURL          func(string) string
	Credentials       func() (cookies, token string)
	AuthSession       func() (userID, cookies, token string)
	DeviceIdentity    func() (deviceID, installID, cdid, openudid string)
	Runtime           func() (userAgent, storeRegion string)
	HTTPClient        *http.Client
	Episodes          func(ctx context.Context, seriesID string) (*hgapi.Page[model.Episode], error)
	SeriesBatch       func(ctx context.Context, seriesIDs []string) ([]model.SeriesDetail, error)
	Series            func(ctx context.Context, seriesID string) (*model.Series, error)
	InvalidateCaches  func(prefixes ...string)
	InvalidateHistory func()
	Notify            func(ctx context.Context, notification Notification)
}

type Service struct {
	store             *userstore.Store
	client            *hgapi.Client
	imageURL          func(string) string
	credentials       func() (cookies, token string)
	authSession       func() (userID, cookies, token string)
	deviceIdentity    func() (deviceID, installID, cdid, openudid string)
	runtimeInfo       func() (userAgent, storeRegion string)
	httpClient        *http.Client
	episodes          func(ctx context.Context, seriesID string) (*hgapi.Page[model.Episode], error)
	seriesBatch       func(ctx context.Context, seriesIDs []string) ([]model.SeriesDetail, error)
	series            func(ctx context.Context, seriesID string) (*model.Series, error)
	invalidateCaches  func(prefixes ...string)
	invalidateHistory func()
	notify            func(ctx context.Context, notification Notification)

	historyMu              sync.Mutex
	historyRemoteMu        sync.Mutex
	cachedHistory          []HistoryItem
	historyCachedAt        time.Time
	historySyncing         bool
	historySyncDone        chan struct{}
	historySyncErr         error
	historyGeneration      uint64
	historyMutationAt      time.Time
	historyMutationMu      sync.Mutex
	historyMutationSyncing bool
	historyMutationWake    bool
	historyMutationErr     error
	historyRetryMu         sync.Mutex
	historyUploadRetries   map[string]historyUploadRetry
	historyScopeRetries    map[string]historyUploadRetry
	historyRetryTimer      *time.Timer
	historyRetryEpoch      uint64
	historyRetryClosed     bool
	historyMetadataMu      sync.Mutex
	historyMetadataSyncing bool
	historyMetadataWake    bool
	historyMetadataTimer   *time.Timer

	favMu           sync.Mutex
	favRemoteMu     sync.Mutex
	cachedFavorites []FavoriteItem
	favCachedAt     time.Time
	favSyncing      bool
	favGeneration   uint64
	favMutationAt   time.Time
	favMutationMu   sync.Mutex
	favSyncMu       sync.Mutex

	likeMu         sync.Mutex
	likeRemoteMu   sync.Mutex
	likeMutationMu sync.Mutex
	cachedLikes    []LikeItem
	likeCachedAt   time.Time
	likeSyncing    bool
	likeGeneration uint64
	background     lifecycle.Group

	resMu              sync.Mutex
	resRemoteMu        sync.Mutex
	cachedReservations []ReservationItem
	resCachedAt        time.Time
	resSyncing         bool
	resGeneration      uint64
}

func (s *Service) WaitForBackgroundSync() { s.background.Wait() }

func (s *Service) Close() {
	s.historyRetryMu.Lock()
	s.historyRetryClosed = true
	s.historyRetryEpoch++
	if s.historyRetryTimer != nil {
		s.historyRetryTimer.Stop()
		s.historyRetryTimer = nil
	}
	s.historyRetryMu.Unlock()
	s.historyMetadataMu.Lock()
	if s.historyMetadataTimer != nil {
		s.historyMetadataTimer.Stop()
		s.historyMetadataTimer = nil
	}
	s.historyMetadataMu.Unlock()
	s.background.Close()
}

func NewService(deps Dependencies) *Service {
	if deps.ImageURL == nil {
		deps.ImageURL = func(raw string) string { return raw }
	}
	if deps.Credentials == nil {
		deps.Credentials = func() (string, string) { return "", "" }
	}
	if deps.DeviceIdentity == nil {
		deps.DeviceIdentity = func() (string, string, string, string) { return "", "", "", "" }
	}
	if deps.Runtime == nil {
		deps.Runtime = func() (string, string) { return "", "" }
	}
	if deps.HTTPClient == nil {
		deps.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if deps.Episodes == nil {
		deps.Episodes = func(context.Context, string) (*hgapi.Page[model.Episode], error) {
			return nil, errors.New("episode provider unavailable")
		}
	}
	if deps.SeriesBatch == nil && deps.Client != nil {
		deps.SeriesBatch = deps.Client.GetSeriesBatch
	}
	if deps.Series == nil && deps.Client != nil {
		deps.Series = deps.Client.GetSeries
	}
	if deps.InvalidateCaches == nil {
		deps.InvalidateCaches = func(prefixes ...string) {}
	}
	if deps.InvalidateHistory == nil {
		deps.InvalidateHistory = func() {}
	}
	if deps.Notify == nil {
		deps.Notify = func(context.Context, Notification) {}
	}
	service := &Service{
		store:             deps.Store,
		client:            deps.Client,
		imageURL:          deps.ImageURL,
		credentials:       deps.Credentials,
		authSession:       deps.AuthSession,
		deviceIdentity:    deps.DeviceIdentity,
		runtimeInfo:       deps.Runtime,
		httpClient:        deps.HTTPClient,
		episodes:          deps.Episodes,
		seriesBatch:       deps.SeriesBatch,
		series:            deps.Series,
		invalidateCaches:  deps.InvalidateCaches,
		invalidateHistory: deps.InvalidateHistory,
		notify:            deps.Notify,
	}
	if service.store != nil {
		service.store.SetSyncScope(service.currentSyncSession().scope)
		service.schedulePendingHistorySync()
	}
	return service
}

func (s *Service) InvalidateUserData() {
	if s.store != nil {
		s.store.SetSyncScope(s.currentSyncSession().scope)
	}
	s.historyMu.Lock()
	s.cachedHistory = nil
	s.historyCachedAt = time.Time{}
	s.historyGeneration++
	s.historyMu.Unlock()
	s.historyMutationMu.Lock()
	s.historyMutationErr = nil
	s.historyMutationMu.Unlock()

	s.favMu.Lock()
	s.cachedFavorites = nil
	s.favCachedAt = time.Time{}
	s.favGeneration++
	s.favMu.Unlock()

	s.likeMu.Lock()
	s.cachedLikes = nil
	s.likeCachedAt = time.Time{}
	s.likeGeneration++
	s.likeMu.Unlock()

	s.resMu.Lock()
	s.cachedReservations = nil
	s.resCachedAt = time.Time{}
	s.resGeneration++
	s.resMu.Unlock()
	s.schedulePendingHistorySync()
}

func (s *Service) currentHistoryGeneration() uint64 {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	return s.historyGeneration
}

func (s *Service) currentFavoriteGeneration() uint64 {
	s.favMu.Lock()
	defer s.favMu.Unlock()
	return s.favGeneration
}

func (s *Service) currentLikeGeneration() uint64 {
	s.likeMu.Lock()
	defer s.likeMu.Unlock()
	return s.likeGeneration
}

func (s *Service) currentReservationGeneration() uint64 {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	return s.resGeneration
}

func (s *Service) touchHistoryMutation() {
	s.historyMu.Lock()
	s.historyMutationAt = time.Now()
	s.historyMu.Unlock()
}

func (s *Service) historySnapshotSettled() bool {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	return time.Since(s.historyMutationAt) >= remoteSnapshotSettleDelay
}

func (s *Service) touchFavoriteMutation() {
	s.favMu.Lock()
	s.favMutationAt = time.Now()
	s.favMu.Unlock()
}

func (s *Service) favoriteSnapshotSettled() bool {
	s.favMu.Lock()
	defer s.favMu.Unlock()
	return time.Since(s.favMutationAt) >= remoteSnapshotSettleDelay
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
