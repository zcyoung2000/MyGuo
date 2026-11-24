package uiapp

import (
	"context"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"myguo/internal/ipc"
	"myguo/internal/native"
)

const serviceTimeout = 15 * time.Second

type CatalogItem struct {
	Kind                                   string
	BadgeText, Author, Status              string
	EpisodeID                              string
	ActorID                                string
	Upcoming, Reserved, Completed, Watched bool
	PublishAt                              string
	UpdatedAt                              int64
	Progress                               float64
	DurationSec                            int64
	ResumeEpisode                          int
	ResumePosition                         float64
	ID                                     string
	Title                                  string
	Description                            string
	CoverURL                               string
	EpisodeCount                           int
	Category                               string
	Rank                                   int
	Heat                                   string
	Subtitles                              []string
}

type CatalogPage struct {
	Items   []CatalogItem
	Cursor  string
	HasMore bool
	Loading bool
	Error   string
}

type Detail struct {
	Source               *ipc.SourceNovelDTO
	Actors               []ipc.ActorInfo
	Upcoming             bool
	Heat                 string
	FollowCount          int64
	LikeCount            int64
	PublishText          string
	Seasons, Adaptations DetailCollection
	Reviews              DetailReviews
	ID                   string
	Title                string
	Description          string
	CoverURL             string
	Tags                 []string
	StatusText           string
	Category             string
	PlayCount            int64
	EpisodeCount         int
	Episodes             []CatalogItem
	Favorite             bool
	Reserved             bool
	Loading              bool
	Error                string
}

type FilterOption struct {
	Label string
	Value string
}

type FilterRow struct {
	Title   string
	Key     string
	Options []FilterOption
}

type Library struct {
	Title       string
	Description string
	Empty       bool
	Loaded      bool
	Loading     bool
	Error       string
	Items       []CatalogItem
}

type Catalog struct {
	FiltersLoading bool
	FiltersLoaded  bool
	FilterError    string
	resultsCurrent bool
	resultGenre    string
	GenreIndex     int
	Genre          []string
	Filters        []FilterRow
	Selection      []int
	Loading        bool
	LoadingMore    bool
	Error          string
	HasMore        bool
	Cursor         string
	seenCursors    map[string]bool
	emptyPages     int
	Items          []CatalogItem
}

type discoverSection int

const (
	discoverExpanded discoverSection = iota
	discoverCollapsed
)

type LibraryTab int

const (
	libraryHistory LibraryTab = iota
	libraryFavorites
	libraryLikes
	libraryReservations
)

type LibraryView struct {
	Tab       LibraryTab
	History   Library
	Favorites Library
	Reserved  Library
	Likes     Library
	Grids     [4]*ui.GridState
	Grid      int
	Selected  int
}

type Settings struct {
	Profile        ipc.UserProfile
	CacheSize      int64
	LegacyNickname string
	HasLegacy      bool
	Loading        bool
	LoginOpen      bool
	QR             QRLoginState
	Success        string
	Error          string
}

// App state and all render snapshots are protected by mu. Network work never holds it.
type App struct {
	playerFullscreen            bool
	playerEpisodesList          ui.ListState
	playerCommentsList          ui.ListState
	playerDanmakuList           ui.ListState
	playerDanmaku               danmakuLayout
	playerDanmakuHover          danmakuHover
	playerPointer               ui.Point
	playerPointerScene          uint64
	playerPointerKnown          bool
	playerFrameKey              string
	playerNativeBackdrop        bool
	playerTransitionBitmap      *ui.Bitmap
	ambientActive               bool
	ambientTop                  ui.Color
	feedChannel                 string
	feedChrome                  FeedChrome
	feedChromeKnown             bool
	discoverPageScroll          ui.ScrollState
	catalogScroll               ui.ScrollState
	rankScroll                  ui.ScrollState
	rankScrollReset             bool
	actorScroll                 ui.ScrollState
	playerFixture               bool
	detailEpisodesList          *ui.ListState
	detailTab                   int
	detailDescriptionOpen       bool
	detailDescriptionLayout     descriptionLayout
	detailScroll                ui.ScrollState
	detailScrollReset           bool
	catalogScrollReset          bool
	sidebarWidth                float32
	preferencesPath             string
	preferencesLoaded           bool
	preferencesPending          sync.WaitGroup
	preferencesMu               sync.Mutex
	media                       *MediaHost
	mediaOn                     bool
	mediaSession                int64
	featuresSource              featureProvider
	reviewsSource               detailReviewProvider
	libraryMutations            libraryMutations
	confirm                     func(string) bool
	mutations                   map[string]bool
	settingsMutations           map[string]bool
	detailRevisions             map[string]uint64
	detailAdaptationKeys        map[string]string
	detailSupplementLoads       map[string]bool
	resumeSequence              uint64
	rank                        RankingState
	releases                    ReleaseState
	actor                       ActorState
	libraryVisible              [4][]CatalogItem
	searchGrid                  *ui.GridState
	pageLayouts                 map[string]pageFillVersion
	catalogList                 *ui.ListState
	discoverFilterState         discoverSection
	discoverFilterManual        bool
	expandedFilters             map[string]bool
	catalogResultVersion        uint64
	searchResultVersion         uint64
	rankResultVersion           uint64
	releaseResultVersion        uint64
	libraryResultVersion        [4]uint64
	releaseGrid                 *ui.GridState
	actorWorksList              *ui.ListState
	suggestions                 []string
	suggestionVersion           uint64
	suggestionIndex             int
	historyFilter, historyQuery string
	reservationsPending         bool
	navigation                  []navigationEntry
	navIndex                    int
	mu                          sync.Mutex
	generation                  uint64
	catalogFilterGeneration     uint64
	catalogFacets               map[string]catalogFacet
	catalogGeneration           uint64
	libraryGeneration           [4]uint64
	started                     bool
	resume                      CatalogItem
	resumeFromPlayback          bool
	service                     *ipc.HongguoService
	artwork                     *artworkLoader
	invalidateFn                func()
	testMode                    bool
	favorite                    favoriteProvider
	detailSource                detailProvider

	view     string
	seriesID string
	loading  bool
	error    string
	notice   string

	query                string
	search               CatalogPage
	catalog              Catalog
	detail               Detail
	libraryData          libraryProvider
	settingsTestProvider settingsProvider
	library              LibraryView
	loginCancel          context.CancelFunc
	loginVersion         uint64
	qrPollInterval       time.Duration
	settings             Settings
	playerOn             bool
}

func NewApp(service *ipc.HongguoService) *App {
	return &App{
		service:    service,
		view:       "discover",
		catalog:    Catalog{Genre: []string{"真人剧", "漫剧", "AI剧"}, HasMore: true},
		rank:       RankingState{Content: "all", Board: "ranklist_hot_sc", HasMore: true},
		navigation: []navigationEntry{{View: "discover"}},
		navIndex:   0,
	}
}

type libraryProvider interface {
	GetHistory(ctx context.Context) ([]ipc.HistoryItem, error)
	GetFavorites(ctx context.Context) ([]ipc.FavoriteItem, error)
	GetReservations(ctx context.Context) ([]ipc.ReservationItem, error)
	GetLikes(ctx context.Context) ([]ipc.LikeItem, error)
}

type favoriteProvider interface {
	ToggleFavorite(ctx context.Context, seriesID, title, coverURL, desc string, totalEpisodes int) (bool, error)
	ToggleReservation(ctx context.Context, seriesID, title, coverURL, note, publishAt string) (bool, error)
}

type detailProvider interface {
	GetSeriesPage(ctx context.Context, seriesID string) (*ipc.SeriesDetailPageDTO, error)
	GetFilterPanel(ctx context.Context, genre string) ([]ipc.FilterRowDTO, error)
	GetDiscover(ctx context.Context, genre string, selections map[string]string, cursor string) (ipc.DiscoverResponse, error)
}

func NewTestApp() *App {
	return &App{
		view:     "discover",
		catalog:  Catalog{Genre: []string{"真人剧", "漫剧", "AI剧"}, HasMore: true},
		rank:     RankingState{Content: "all", Board: "ranklist_hot_sc", HasMore: true},
		testMode: true,
	}
}

func NewLibraryGrids() [4]*ui.GridState {
	return [4]*ui.GridState{{}, {}, {}, {}}
}

func (a *App) SetInvalidator(fn func()) {
	a.mu.Lock()
	a.invalidateFn = fn
	a.mu.Unlock()
}
func (a *App) SetLibraryGrids(grids [4]*ui.GridState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.library.Grids = grids
}
func (a *App) SetAmbientGlow(active bool, _ [3]uint8, top [3]uint8) {
	a.mu.Lock()
	a.ambientActive = active
	if active {
		a.ambientTop = ui.RGB(top[0], top[1], top[2])
	}
	invalidate := a.invalidateFn
	a.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
}
func (a *App) View() string              { a.mu.Lock(); defer a.mu.Unlock(); return a.view }
func (a *App) Query() string             { a.mu.Lock(); defer a.mu.Unlock(); return a.query }
func (a *App) GlobalError() string       { a.mu.Lock(); defer a.mu.Unlock(); return a.error }
func (a *App) Notice() string            { a.mu.Lock(); defer a.mu.Unlock(); return a.notice }
func (a *App) PlayerVisible() bool       { a.mu.Lock(); defer a.mu.Unlock(); return a.mediaOn }
func (a *App) SearchState() CatalogPage  { a.mu.Lock(); defer a.mu.Unlock(); return a.search }
func (a *App) CatalogState() Catalog     { a.mu.Lock(); defer a.mu.Unlock(); return a.catalog }
func (a *App) LibraryState() LibraryView { a.mu.Lock(); defer a.mu.Unlock(); return a.library }
func (a *App) DetailState() Detail       { a.mu.Lock(); defer a.mu.Unlock(); return a.detail }
func (a *App) SettingsState() Settings   { a.mu.Lock(); defer a.mu.Unlock(); return a.settings }
func (a *App) SeriesID() string          { a.mu.Lock(); defer a.mu.Unlock(); return a.seriesID }

func (a *App) SearchSubmitted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	term := strings.TrimSpace(a.query)
	if term == "" {
		return false
	}
	a.navigateLocked(navigationEntry{View: "search"}, true)
	a.suggestions = nil
	a.suggestionVersion++
	a.search.Loading, a.search.Error = true, ""
	generation := a.generation
	if !a.testMode {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
			defer cancel()
			items, err := a.service.Search(ctx, term)
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.generation != generation {
				return
			}
			a.search.Loading = false
			if err != nil {
				a.search.Error = featureError("search", err)
			} else {
				a.search = CatalogPage{Items: removeSearchPageCardLabels(convertCards(items))}
				a.searchResultVersion++
			}
			a.invalidate()
		}()
	}
	return true
}
func (a *App) SearchChanged() bool { return false }
func (a *App) SetQuery(value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if value == a.query {
		return
	}
	a.query = value
	a.suggestionVersion++
	a.suggestions = nil
	a.suggestionIndex = -1
	if strings.TrimSpace(value) != "" && !a.testMode {
		go a.suggest(value, a.suggestionVersion)
	}
	a.invalidate()
}
func (a *App) OpenView(view string) {
	log.Printf("[uiapp] OpenView target=%s current=%s", view, a.view)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.view == view && a.started {
		if view == "recommend" && a.mediaOn {
			a.closeMediaLocked()
		}
		return
	}
	a.navigateLocked(navigationEntry{View: view}, true)
}
func (a *App) OpenDetail(seriesID string) { a.OpenItem(CatalogItem{ID: seriesID}) }
func (a *App) OpenItem(item CatalogItem) {
	if item.ActorID != "" {
		a.OpenActor(item.ActorID)
		return
	}
	if item.ID == "" || item.Kind == "source_novel" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.navigateLocked(navigationEntry{View: "detail", Item: item}, true)
}
func (a *App) ToggleDetailFavorite()    { a.toggleDetail(false) }
func (a *App) ToggleDetailReservation() { a.toggleDetail(true) }
func (a *App) toggleDetail(reservation bool) {
	a.mu.Lock()
	detail, generation, provider := a.detail, a.generation, a.favoriteProvider()
	kind := "favorite"
	if reservation {
		kind = "reservation"
	}
	key := kind + ":" + detail.ID
	if detail.ID == "" || detail.Loading || !a.beginMutationLocked(key) {
		a.mu.Unlock()
		return
	}
	confirm := a.confirm
	a.mu.Unlock()
	go func() {
		if reservation && detail.Reserved && confirm != nil && !confirm("确认取消预约《"+detail.Title+"》？") {
			a.mu.Lock()
			a.endMutationLocked(key)
			a.mu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
		defer cancel()
		var enabled bool
		var err error
		if reservation {
			enabled, err = provider.ToggleReservation(ctx, detail.ID, detail.Title, detail.CoverURL, detail.Description, "")
		} else {
			enabled, err = provider.ToggleFavorite(ctx, detail.ID, detail.Title, detail.CoverURL, detail.Description, detail.EpisodeCount)
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		a.endMutationLocked(key)
		if err != nil {
			if a.generation == generation {
				a.fail("更新失败：", err)
			}
			return
		}
		if reservation {
			a.applyReservationLocked(detail.ID, enabled)
		} else if a.detail.ID == detail.ID {
			a.detail.Favorite = enabled
		}
		a.invalidate()
	}()
}
func (a *App) favoriteProvider() favoriteProvider {
	if a.favorite != nil {
		return a.favorite
	}
	return a.service
}
func (a *App) ChooseEpisode(num int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	loaded := num > 0 && a.media != nil && !a.detail.Loading && a.detail.ID != "" && !a.detail.Upcoming
	if loaded {
		a.detailScrollReset = true
	}
	if !loaded {
		return
	}
	a.playLocked(num, -1)
}
func (a *App) OpenRelated(seriesID string) { a.OpenDetail(seriesID) }
func (a *App) LoadMoreCatalog() {
	if a.testMode && a.detailProvider() == nil {
		return
	}
	a.loadCatalog(true)
}
func (a *App) LoadMoreSearch() bool { return false }
func (a *App) RefreshLibrary(tab LibraryTab) {
	if !a.testMode {
		go a.loadLibraryWithRefresh(tab, true)
	}
}
func (a *App) PlayerResume() { a.MediaCommand(native.MediaCommandPayload{Command: "play"}) }
func (a *App) PlayerPause()  { a.MediaCommand(native.MediaCommandPayload{Command: "pause"}) }

func (a *App) PlayerPrevious() { a.MediaCommand(native.MediaCommandPayload{Command: "prev"}) }

func (a *App) PlayerNext() { a.MediaCommand(native.MediaCommandPayload{Command: "next"}) }
func (a *App) PlayerPictureInPicture() {
	a.MediaCommand(native.MediaCommandPayload{Command: "pip"})
}
func (a *App) loadDetail(seriesID string) {
	a.mu.Lock()
	generation := a.generation
	a.mu.Unlock()
	a.loadDetailAt(seriesID, generation)
}
func (a *App) loadDetailAt(seriesID string, generation uint64) {
	a.mu.Lock()
	revision := a.detailRevisions[seriesID]
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), serviceTimeout)
	defer cancel()
	page, err := a.detailProvider().GetSeriesPage(ctx, seriesID)
	a.mu.Lock()
	if generation != a.generation {
		a.mu.Unlock()
		return
	}
	if revision != a.detailRevisions[seriesID] {
		a.mu.Unlock()
		go a.loadDetailAt(seriesID, generation)
		return
	}
	if err != nil || page == nil {
		a.detail.Loading, a.detail.Error = false, "详情加载失败，请稍后重试"
		a.invalidate()
		a.mu.Unlock()
		return
	}
	// Metadata can arrive after the user has already opened the reviews tab.
	// Keep that independent request and its expanded replies across a refresh.
	reviews := DetailReviews{}
	viewed := make(map[int]bool)
	if a.detail.ID == seriesID {
		reviews = a.detail.Reviews
		if a.resumeFromPlayback {
			for _, ep := range a.detail.Episodes {
				if ep.Watched {
					viewed[ep.EpisodeCount] = true
				}
			}
		}
	}
	a.detail = convertDetail(seriesID, page)
	a.detail.Reviews = reviews
	for index := range a.detail.Episodes {
		ep := &a.detail.Episodes[index]
		ep.Watched = ep.Watched || viewed[ep.EpisodeCount]
	}
	if a.resumeFromPlayback && a.resume.ID == seriesID && a.resume.ResumePosition > 0 {
		a.markDetailEpisodeWatchedLocked(a.resume.ResumeEpisode)
	}
	if page.ResumeEpisode > 0 && (a.resume.ID != seriesID || !a.resumeFromPlayback) {
		a.resume = CatalogItem{ID: seriesID, ResumeEpisode: page.ResumeEpisode, ResumePosition: page.ResumePosition}
	}
	a.invalidate()
	a.mu.Unlock()
	if a.featuresSource != nil || a.service != nil {
		go a.loadSupplement(seriesID, generation, true)
	}
}
func removeSearchPageCardLabels(items []CatalogItem) []CatalogItem {
	for index := range items {
		items[index].BadgeText = ""
		items[index].Category = ""
	}
	return items
}

func (a *App) detailProvider() detailProvider {
	if a.detailSource != nil {
		return a.detailSource
	}
	if a.service != nil {
		return a.service
	}
	return nil
}
func (a *App) libraryAt(tab LibraryTab) *Library {
	switch tab {
	case libraryHistory:
		return &a.library.History
	case libraryFavorites:
		return &a.library.Favorites
	case libraryLikes:
		return &a.library.Likes
	default:
		return &a.library.Reserved
	}
}

// Both tabs share a header, so entering either must load both real datasets.
func (a *App) loadLibrary(tab LibraryTab) {
	if tab == libraryFavorites || tab == libraryLikes {
		var pending sync.WaitGroup
		for _, collection := range []LibraryTab{libraryFavorites, libraryLikes} {
			pending.Add(1)
			go func(tab LibraryTab) {
				defer pending.Done()
				a.loadLibraryTab(tab)
			}(collection)
		}
		pending.Wait()
		return
	}
	a.loadLibraryTab(tab)
}

func libraryCountLabel(library Library) string {
	if library.Loaded || len(library.Items) > 0 || library.Empty {
		return strconv.Itoa(len(library.Items))
	}
	if library.Error != "" {
		return "—"
	}
	return "…"
}

func (a *App) loadLibraryTab(tab LibraryTab) {
	a.loadLibraryWithRefresh(tab, false)
	if !a.testMode && a.service != nil {
		a.loadLibraryWithRefresh(tab, true)
	}
}
func (a *App) loadLibraryWithRefresh(tab LibraryTab, refresh bool) {
	a.mu.Lock()
	a.libraryGeneration[tab]++
	generation := a.libraryGeneration[tab]
	library := a.libraryAt(tab)
	library.Loading, library.Error, library.Empty = true, "", false
	a.refreshVisibleLibraryLocked(tab)
	provider := a.libraryProvider()
	a.invalidate()
	a.mu.Unlock()
	timeout := serviceTimeout
	if tab == libraryHistory && refresh {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var items []CatalogItem
	var err error
	if refresher, ok := provider.(libraryRefresher); refresh && ok {
		switch tab {
		case libraryHistory:
			items, err = convertHistory(refresher.RefreshHistory(ctx))
		case libraryFavorites:
			items, err = convertFavorites(refresher.RefreshFavorites(ctx))
		case libraryLikes:
			items, err = convertLikes(refresher.RefreshLikes(ctx))
		default:
			items, err = convertReservations(refresher.RefreshReservations(ctx))
		}
	} else {
		switch tab {
		case libraryHistory:
			items, err = convertHistory(provider.GetHistory(ctx))
		case libraryFavorites:
			items, err = convertFavorites(provider.GetFavorites(ctx))
		case libraryLikes:
			items, err = convertLikes(provider.GetLikes(ctx))
		default:
			items, err = convertReservations(provider.GetReservations(ctx))
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if generation != a.libraryGeneration[tab] {
		return
	}
	library = a.libraryAt(tab)
	library.Loading = false
	if err != nil {
		// History synchronization is durable and self-healing: local rows and
		// pending uploads remain available while the background worker retries.
		// Keep transport and account-maintenance details out of the history UI.
		library.Error = ""
		if tab != libraryHistory {
			library.Error = "数据加载失败，请稍后重试"
		} else if len(items) == 0 {
			library.Error = "暂时无法读取观看历史，请稍后重试"
		}
		log.Printf("[library] tab=%d load failed: %v", tab, err)
		if len(items) == 0 && tab != libraryHistory {
			a.invalidate()
			return
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if tab == libraryReservations {
			return reservationBefore(items[i], items[j])
		}
		return items[i].Rank < items[j].Rank
	})
	a.error = ""
	library.Items, library.Empty, library.Loaded = items, len(items) == 0, true
	library.Title = map[LibraryTab]string{libraryHistory: "观看历史", libraryFavorites: "我的收藏", libraryLikes: "我的点赞", libraryReservations: "新剧预约"}[tab]
	a.refreshVisibleLibraryLocked(tab)
	a.invalidate()
}

func (a *App) libraryProvider() libraryProvider {
	if a.libraryData != nil {
		return a.libraryData
	}
	return a.service
}

// open, fail, setNotice and invalidate require mu. Player commands enqueue work and never wait for the UI thread.
func (a *App) open(view, seriesID string) {
	a.generation++
	a.view, a.seriesID, a.error, a.loading, a.playerOn = view, seriesID, "", false, false
	a.notice = ""
	a.resetResumeLocked(seriesID)
	a.mediaOn = false
	if a.media != nil {
		a.media.Stop()
	}
	a.detail = Detail{}
	a.detailTab = 0
	a.detailDescriptionOpen = false
	a.detailEpisodesList = nil
	a.detailScrollReset = true
	a.invalidate()
}
func (a *App) fail(prefix string, err error) {
	a.error = prefix + featureError("", err)
	a.invalidate()
}
func (a *App) setNotice(message string) { a.notice = message; a.invalidate() }
func (a *App) invalidate() {
	if a.invalidateFn != nil {
		a.invalidateFn()
	}
}

func convertCards(cards []ipc.SeriesCard) []CatalogItem {
	items := make([]CatalogItem, 0, len(cards))
	for _, card := range cards {
		items = append(items, CatalogItem{
			Kind: card.Kind, BadgeText: card.BadgeText, Author: card.Author, Status: card.Status, ID: card.ID, Title: card.Title, Description: card.Description, CoverURL: card.CoverURL,
			EpisodeCount: card.EpisodeCount, Category: card.Category, Heat: card.Heat, Subtitles: card.Tags, Upcoming: card.Upcoming, Reserved: card.Reserved, ActorID: actorCardID(card),
		})
	}
	return items
}

func convertHistory(items []ipc.HistoryItem, err error) ([]CatalogItem, error) {
	out := make([]CatalogItem, 0, len(items))
	for index, item := range items {
		title := item.Title
		if title == "" || isHistoryPlaceholderTitle(title) {
			title = "剧名待补全 · " + item.SeriesID
		}
		out = append(out, CatalogItem{
			ID: item.SeriesID, Title: title,
			CoverURL: item.CoverURL, EpisodeCount: item.TotalEpisodes, Rank: index,
			ResumeEpisode: item.EpisodeNum, ResumePosition: item.PositionSec,
			UpdatedAt: item.UpdatedAt, Progress: historyPercent(item.PositionSec, float64(item.DurationSec)), Completed: item.TotalEpisodes > 0 && item.EpisodeNum == item.TotalEpisodes && historyPercent(item.PositionSec, float64(item.DurationSec)) >= 95,
		})
	}
	return out, err
}

func isHistoryPlaceholderTitle(title string) bool {
	title = strings.TrimSpace(title)
	if !strings.HasPrefix(title, "第") || !strings.HasSuffix(title, "集") {
		return false
	}
	_, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(title, "第"), "集"))
	return err == nil
}

func convertFavorites(items []ipc.FavoriteItem, err error) ([]CatalogItem, error) {
	if err != nil {
		return nil, err
	}
	out := make([]CatalogItem, 0, len(items))
	for index, item := range items {
		title := item.Title
		if title == "" {
			title = "未知短剧"
		}
		out = append(out, CatalogItem{
			ID: item.SeriesID, Title: title, Description: item.Description, CoverURL: item.CoverURL,
			EpisodeCount: item.TotalEpisodes, Rank: index,
		})
	}
	return out, nil
}

func convertReservations(items []ipc.ReservationItem, err error) ([]CatalogItem, error) {
	if err != nil {
		return nil, err
	}
	out := make([]CatalogItem, 0, len(items))
	for index, item := range items {
		title := item.Title
		if title == "" {
			title = "未知短剧"
		}
		out = append(out, CatalogItem{
			ID: item.SeriesID, Title: title, Description: item.Note, CoverURL: item.CoverURL,
			PublishAt: item.PublishAt, Upcoming: !item.IsOnline, Reserved: true, Rank: index,
		})
	}
	return out, nil
}

func convertLikes(items []ipc.LikeItem, err error) ([]CatalogItem, error) {
	if err != nil {
		return nil, err
	}
	out := make([]CatalogItem, 0, len(items))
	for index, item := range items {
		title := item.Title
		if title == "" {
			title = "未知短剧"
		}
		out = append(out, CatalogItem{
			ID: item.SeriesID, EpisodeID: item.EpisodeID, ResumeEpisode: item.EpisodeNum, ResumePosition: -1, Title: title, Description: item.Description, CoverURL: item.CoverURL,
			EpisodeCount: item.TotalEpisodes, Rank: index,
		})
	}
	return out, nil
}

func convertDetail(seriesID string, page *ipc.SeriesDetailPageDTO) Detail {
	episodes := make([]CatalogItem, 0, len(page.Episodes))
	for _, episode := range page.Episodes {
		episodes = append(episodes, CatalogItem{
			ID: episode.ID, Title: episode.Title, CoverURL: episode.PosterURL,
			EpisodeCount: episode.Number, Rank: episode.Number,
			DurationSec: episode.DurationSec, Watched: episode.Watched,
		})
	}
	return Detail{
		ID: seriesID, Title: page.Title, Description: page.Description, CoverURL: page.CoverURL,
		Source: page.Source, Actors: page.Actors, Upcoming: page.Upcoming || page.EpisodeCount == 0, Heat: page.Heat, FollowCount: page.FollowCount, LikeCount: page.LikeCount, PublishText: page.PublishText, Seasons: DetailCollection{Loading: true}, Adaptations: DetailCollection{Loading: true},
		Tags: page.Tags, StatusText: page.StatusText, Category: page.Category, PlayCount: page.PlayCount,
		EpisodeCount: page.EpisodeCount, Episodes: episodes, Favorite: page.IsFavorite, Reserved: page.Reserved,
	}
}

func historyPercent(position, duration float64) float64 {
	if duration <= 0 {
		return 0
	}
	value := position * 100 / duration
	if value > 100 {
		return 100
	}
	if value < 0 {
		return 0
	}
	return value
}
func actorCardID(card ipc.SeriesCard) string {
	if card.Kind == "actor" {
		return card.ID
	}
	return ""
}

// Invalidate may be called by the playback event loop without holding App.mu.
func (a *App) Invalidate() {
	a.mu.Lock()
	fn := a.invalidateFn
	a.mu.Unlock()
	if fn != nil {
		fn()
	}
}
func (a *App) SetPlayerFullscreen(value bool) {
	a.mu.Lock()
	a.playerFullscreen = value
	a.invalidate()
	a.mu.Unlock()
}

func (a *App) PlaybackChanged() {
	a.mu.Lock()
	host := a.media
	fn := a.invalidateFn
	a.mu.Unlock()
	if host != nil {
		s := host.Snapshot()
		top := [3]uint8{}
		active := s.Mode != "idle" && s.Status.Ready && len(s.Status.AmbientTop) == 3
		if active {
			for i := range 3 {
				top[i] = uint8(s.Status.AmbientTop[i])
			}
		}
		a.mu.Lock()
		a.ambientActive = active
		if active {
			a.ambientTop = ui.RGB(top[0], top[1], top[2])
		}
		a.mu.Unlock()
	}
	if fn != nil {
		fn()
	}
}
