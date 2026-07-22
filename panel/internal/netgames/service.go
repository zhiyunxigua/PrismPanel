package netgames

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"PrismPanel/internal/store"
)

type SeriesPoint struct {
	RunID     uint64    `json:"run_id"`
	SampledAt time.Time `json:"sampled_at"`
	Value     *uint32   `json:"value,omitempty"`
}

type SeriesGame struct {
	GameID            string        `json:"game_id"`
	Name              string        `json:"name"`
	Rank              int           `json:"rank"`
	ColorRank         int           `json:"color_rank"`
	LatestOnlineCount uint32        `json:"latest_online_count"`
	Points            []SeriesPoint `json:"points"`
}

type SeriesResponse struct {
	WindowStart time.Time                    `json:"window_start"`
	WindowEnd   time.Time                    `json:"window_end"`
	Runs        []store.NetGameCollectionRun `json:"runs"`
	Games       []SeriesGame                 `json:"games"`
}

type GameResponse struct {
	Game              store.NetGame                `json:"game"`
	Rank              *int                         `json:"rank,omitempty"`
	LatestOnlineCount *uint32                      `json:"latest_online_count,omitempty"`
	Runs              []store.NetGameCollectionRun `json:"runs"`
	Points            []SeriesPoint                `json:"points"`
}

type Service struct {
	store        *store.Store
	state        *StateStore
	logger       *slog.Logger
	collectMutex chan struct{}
	detailMutex  chan struct{}
	stopOnce     sync.Once
}

func NewService(repository *store.Store, masterKey []byte, baseDir string, logger *slog.Logger) (*Service, error) {
	state, err := NewStateStore(baseDir, masterKey)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store: repository, state: state, logger: logger,
		collectMutex: make(chan struct{}, 1), detailMutex: make(chan struct{}, 1),
	}, nil
}

func (s *Service) Start(ctx context.Context) {
	if err := s.store.MarkInterruptedNetGameRuns(ctx); err != nil {
		s.logger.Warn("mark interrupted net game runs", "error", err)
	}
	if _, err := s.state.Account(); err == nil {
		go s.collectFullOnce(context.Background(), "startup")
	}
	go s.collectionLoop(ctx)
	go s.detailLoop(ctx)
}

func (s *Service) Account(ctx context.Context) (AccountView, error) {
	account, err := s.state.Account()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AccountView{}, err
		}
		if errors.Is(err, os.ErrNotExist) {
			return AccountView{HasAccount: false}, nil
		}
		return AccountView{}, err
	}
	view := AccountView{Email: account.Email, HasAccount: true, VerifiedAt: account.VerifiedAt}
	if !account.CreatedAt.IsZero() {
		created := account.CreatedAt
		view.CreatedAt = &created
	}
	if !account.UpdatedAt.IsZero() {
		updated := account.UpdatedAt
		view.UpdatedAt = &updated
	}
	return view, nil
}

func (s *Service) Settings() Settings {
	return s.state.Settings()
}

func (s *Service) UpdateSettings(settings Settings) (Settings, error) {
	if err := s.state.UpdateSettings(settings); err != nil {
		return Settings{}, err
	}
	return s.state.Settings(), nil
}

func (s *Service) SaveAccount(ctx context.Context, email, password string) (AccountView, error) {
	state := AccountState{Email: email, Password: password}
	client, err := NewClient(state)
	if err != nil {
		return AccountView{}, err
	}
	device, err := client.Login(ctx)
	if err != nil {
		return AccountView{}, err
	}
	now := time.Now().UTC()
	state.Device = device
	state.VerifiedAt = &now
	state.CreatedAt = now
	state.UpdatedAt = now
	if err := s.state.SaveAccount(state); err != nil {
		return AccountView{}, err
	}
	go s.collectFullOnce(context.Background(), "account_saved")
	return AccountView{Email: state.Email, HasAccount: true, VerifiedAt: state.VerifiedAt, CreatedAt: &state.CreatedAt, UpdatedAt: &state.UpdatedAt}, nil
}

func (s *Service) DeleteAccount() error {
	return s.state.DeleteAccount()
}

func (s *Service) CollectorStatus(ctx context.Context) (CollectorStatus, error) {
	account, _ := s.Account(ctx)
	settings := s.Settings()
	lastRun, err := s.store.LatestNetGameRun(ctx, "")
	var lastRunPtr *store.NetGameCollectionRun
	if err == nil {
		value := lastRun
		lastRunPtr = &value
	} else if !errors.Is(err, store.ErrNotFound) {
		return CollectorStatus{}, err
	}
	status := CollectorStatus{
		Running:  false,
		LastRun:  lastRunPtr,
		Account:  account,
		Settings: settings,
	}
	if lastRunPtr != nil {
		status.Running = lastRunPtr.Status == store.NetGameRunRunning
	}
	if next, ok := s.nextRunAt(ctx, settings, lastRunPtr); ok {
		status.NextRunAt = &next
	}
	return status, nil
}

func (s *Service) CollectNow(ctx context.Context, trigger string) (store.NetGameCollectionRun, error) {
	return s.collectOnce(ctx, trigger, true)
}

func (s *Service) RefreshDetails(ctx context.Context, gameID string) (store.NetGame, error) {
	return s.refreshOneDetail(ctx, strings.TrimSpace(gameID))
}

func (s *Service) Series(ctx context.Context, userID string, displayCount int, forcedIDs []string, from, to time.Time) (SeriesResponse, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		to = time.Now().UTC()
		from = to.Add(-24 * time.Hour)
	}
	if displayCount < 1 || displayCount > 20 {
		displayCount = 10
	}
	runs, err := s.store.NetGameRunsBetween(ctx, from, to)
	if err != nil {
		return SeriesResponse{}, err
	}
	latest, err := s.store.LatestSuccessfulNetGameRun(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return SeriesResponse{}, err
	}
	games := make([]SeriesGame, 0)
	if err == nil {
		ranked, rankErr := s.store.RankedGamesForRun(ctx, latest.ID, forcedIDs, displayCount)
		if rankErr != nil {
			return SeriesResponse{}, rankErr
		}
		observationMap := make(map[uint64]map[string]uint32)
		observations, obsErr := s.store.NetGameObservationsBetween(ctx, from, to)
		if obsErr != nil {
			return SeriesResponse{}, obsErr
		}
		for _, observation := range observations {
			bucket := observationMap[observation.RunID]
			if bucket == nil {
				bucket = make(map[string]uint32)
				observationMap[observation.RunID] = bucket
			}
			bucket[observation.GameID] = observation.OnlineCount
		}
		for _, rankedGame := range ranked {
			series := SeriesGame{
				GameID: rankedGame.GameID, Name: rankedGame.Name, Rank: rankedGame.Rank,
				ColorRank: rankedGame.Rank, LatestOnlineCount: rankedGame.OnlineCount,
			}
			series.Points = make([]SeriesPoint, 0, len(runs))
			for _, run := range runs {
				point := SeriesPoint{RunID: run.ID, SampledAt: run.StartedAt}
				if run.Status == store.NetGameRunSuccess {
					if count, ok := observationMap[run.ID][rankedGame.GameID]; ok {
						value := count
						point.Value = &value
					}
				}
				series.Points = append(series.Points, point)
			}
			games = append(games, series)
		}
	}
	return SeriesResponse{WindowStart: from, WindowEnd: to, Runs: runs, Games: games}, nil
}

func (s *Service) Game(ctx context.Context, gameID string, displayCount int, forcedIDs []string, from, to time.Time) (GameResponse, error) {
	game, err := s.store.GetNetGame(ctx, strings.TrimSpace(gameID))
	if err != nil {
		return GameResponse{}, err
	}
	series, err := s.Series(ctx, "", displayCount, forcedIDs, from, to)
	if err != nil {
		return GameResponse{}, err
	}
	var rank *int
	var latest *uint32
	for _, item := range series.Games {
		if item.GameID == game.GameID {
			rankValue := item.Rank
			rank = &rankValue
			count := item.LatestOnlineCount
			latest = &count
			break
		}
	}
	points := make([]SeriesPoint, 0)
	for _, seriesGame := range series.Games {
		if seriesGame.GameID == game.GameID {
			points = seriesGame.Points
			break
		}
	}
	return GameResponse{Game: game, Rank: rank, LatestOnlineCount: latest, Runs: series.Runs, Points: points}, nil
}

func (s *Service) collectionLoop(ctx context.Context) {
	settings := s.Settings()
	ticker := time.NewTicker(time.Duration(settings.CollectionIntervalMinutes) * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.collectOnce(ctx, "scheduled", true); err != nil && !errors.Is(err, ErrCollectorBusy) {
				s.logger.Warn("scheduled net game collection", "error", err)
			}
		}
	}
}

func (s *Service) detailLoop(ctx context.Context) {
	settings := s.Settings()
	ticker := time.NewTicker(time.Duration(settings.DetailRefreshHours) * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !waitNetGameDetailRefreshDelay(ctx) {
				return
			}
			if _, _, err := s.refreshDetailsBatch(ctx, settings.MaxDetailBatchSize); err != nil && !errors.Is(err, ErrCollectorBusy) {
				s.logger.Warn("scheduled net game detail refresh", "error", err)
			}
		}
	}
}

var ErrCollectorBusy = errors.New("net game collector is already running")

const netGameDetailRefreshDelay = 10 * time.Second

func (s *Service) collectFullOnce(ctx context.Context, trigger string) {
	run, client, err := s.collectOnceWithClient(ctx, trigger, false)
	if err != nil {
		if !errors.Is(err, ErrCollectorBusy) {
			s.logger.Warn("full net game collection", "trigger", trigger, "error", err)
		}
		return
	}
	if run.Status != store.NetGameRunSuccess || client == nil {
		return
	}
	if !waitNetGameDetailRefreshDelay(ctx) {
		return
	}
	if err := s.refreshAllDetailsWithClient(ctx, client); err != nil && !errors.Is(err, ErrCollectorBusy) {
		s.logger.Warn("full net game detail refresh", "trigger", trigger, "run_id", run.ID, "error", err)
	}
}

func (s *Service) collectOnce(ctx context.Context, trigger string, refreshDetails bool) (store.NetGameCollectionRun, error) {
	run, _, err := s.collectOnceWithClient(ctx, trigger, refreshDetails)
	return run, err
}

func (s *Service) collectOnceWithClient(ctx context.Context, trigger string, refreshDetails bool) (store.NetGameCollectionRun, *Client, error) {
	select {
	case s.collectMutex <- struct{}{}:
		defer func() { <-s.collectMutex }()
	default:
		return store.NetGameCollectionRun{}, nil, ErrCollectorBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	account, err := s.state.Account()
	if err != nil {
		run, runErr := s.store.CreateNetGameCollectionRun(ctx, trigger)
		if runErr != nil {
			return store.NetGameCollectionRun{}, nil, runErr
		}
		_ = s.store.FailNetGameCollectionRun(ctx, run.ID, "ACCOUNT_UNAVAILABLE", err.Error(), "")
		return run, nil, err
	}
	client, err := NewClient(account)
	if err != nil {
		return store.NetGameCollectionRun{}, nil, err
	}
	run, err := s.store.CreateNetGameCollectionRun(ctx, trigger)
	if err != nil {
		return store.NetGameCollectionRun{}, nil, err
	}
	device, err := client.Login(ctx)
	if err != nil {
		_ = s.store.FailNetGameCollectionRun(ctx, run.ID, errorCode(err), errorMessage(err), "")
		return run, nil, err
	}
	account.Device = device
	account.UpdatedAt = time.Now().UTC()
	if account.VerifiedAt == nil {
		now := time.Now().UTC()
		account.VerifiedAt = &now
	}
	_ = s.state.SaveAccount(account)
	games, pagesFetched, err := client.FetchNetworkGames(ctx, 20, 1000)
	if err != nil {
		_ = s.store.FailNetGameCollectionRun(ctx, run.ID, errorCode(err), errorMessage(err), "")
		return run, nil, err
	}
	run, err = s.store.CompleteNetGameCollectionRun(ctx, run.ID, pagesFetched, uint32(len(games)), games, "")
	if err != nil {
		return store.NetGameCollectionRun{}, nil, err
	}
	if refreshDetails {
		go s.refreshDetailsBatchWithClientDelayed(context.Background(), client, s.Settings().MaxDetailBatchSize, trigger)
	}
	go s.purgeOldHistory(context.Background())
	return run, client, nil
}

func (s *Service) refreshDetailsBatchWithClientDelayed(ctx context.Context, client *Client, limit int, trigger string) {
	if !waitNetGameDetailRefreshDelay(ctx) {
		return
	}
	if _, _, err := s.refreshDetailsBatchWithClient(ctx, client, limit); err != nil && !errors.Is(err, ErrCollectorBusy) {
		s.logger.Warn("delayed net game detail refresh", "trigger", trigger, "error", err)
	}
}

func waitNetGameDetailRefreshDelay(ctx context.Context) bool {
	timer := time.NewTimer(netGameDetailRefreshDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *Service) refreshAllDetails(ctx context.Context) error {
	account, err := s.state.Account()
	if err != nil {
		return err
	}
	client, err := NewClient(account)
	if err != nil {
		return err
	}
	if _, err := client.Login(ctx); err != nil {
		return err
	}
	return s.refreshAllDetailsWithClient(ctx, client)
}

func (s *Service) refreshAllDetailsWithClient(ctx context.Context, client *Client) error {
	batchSize := s.Settings().MaxDetailBatchSize
	if batchSize < 1 {
		batchSize = 50
	}
	for {
		selected, updated, err := s.refreshDetailsBatchWithClient(ctx, client, batchSize)
		if err != nil || selected < batchSize || updated == 0 {
			return err
		}
	}
}

func (s *Service) refreshDetailsBatch(ctx context.Context, limit int) (int, int, error) {
	account, err := s.state.Account()
	if err != nil {
		return 0, 0, err
	}
	client, err := NewClient(account)
	if err != nil {
		return 0, 0, err
	}
	if _, err := client.Login(ctx); err != nil {
		return 0, 0, err
	}
	return s.refreshDetailsBatchWithClient(ctx, client, limit)
}

func (s *Service) refreshDetailsBatchWithClient(ctx context.Context, client *Client, limit int) (int, int, error) {
	if client == nil {
		return 0, 0, errors.New("net game client is not configured")
	}
	select {
	case s.detailMutex <- struct{}{}:
		defer func() { <-s.detailMutex }()
	default:
		return 0, 0, ErrCollectorBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	games, err := s.store.NetGamesNeedingDetails(ctx, limit)
	if err != nil {
		return 0, 0, err
	}
	updated := 0
	for _, game := range games {
		details, detailErr := client.FetchDetails(ctx, game.GameID)
		if detailErr != nil {
			_ = s.store.MarkNetGameDetailsFailed(ctx, game.GameID, errorCode(detailErr), errorMessage(detailErr))
			continue
		}
		if _, detailErr = s.store.UpdateNetGameDetails(ctx, game.GameID, details); detailErr != nil {
			return len(games), updated, detailErr
		}
		updated++
	}
	return len(games), updated, nil
}
func (s *Service) refreshOneDetail(ctx context.Context, gameID string) (store.NetGame, error) {
	if gameID == "" {
		return store.NetGame{}, store.ErrNotFound
	}
	account, err := s.state.Account()
	if err != nil {
		return store.NetGame{}, err
	}
	client, err := NewClient(account)
	if err != nil {
		return store.NetGame{}, err
	}
	if _, err := client.Login(ctx); err != nil {
		return store.NetGame{}, err
	}
	details, err := client.FetchDetails(ctx, gameID)
	if err != nil {
		_ = s.store.MarkNetGameDetailsFailed(ctx, gameID, errorCode(err), errorMessage(err))
		return store.NetGame{}, err
	}
	return s.store.UpdateNetGameDetails(ctx, gameID, details)
}

func (s *Service) purgeOldHistory(ctx context.Context) {
	settings := s.Settings()
	cutoff := time.Now().UTC().Add(-time.Duration(settings.HistoryRetentionDays) * 24 * time.Hour)
	if _, err := s.store.NetGameRunsBetween(ctx, time.Unix(0, 0).UTC(), cutoff); err != nil {
		s.logger.Debug("net game history cleanup skipped", "error", err)
	}
}

func (s *Service) nextRunAt(ctx context.Context, settings Settings, lastRun *store.NetGameCollectionRun) (time.Time, bool) {
	if lastRun == nil {
		return time.Now().UTC(), true
	}
	interval := time.Duration(settings.CollectionIntervalMinutes) * time.Minute
	if lastRun.Status == store.NetGameRunRunning {
		return lastRun.StartedAt.Add(interval), true
	}
	return lastRun.StartedAt.Add(interval), true
}

func errorCode(err error) string {
	var protocol *ProtocolError
	if errors.As(err, &protocol) && protocol.Code != "" {
		return protocol.Code
	}
	return "INTERNAL"
}

func errorMessage(err error) string {
	var protocol *ProtocolError
	if errors.As(err, &protocol) && protocol.Message != "" {
		return protocol.Message
	}
	return strings.TrimSpace(err.Error())
}
