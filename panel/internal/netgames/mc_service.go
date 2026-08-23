package netgames

import (
	"context"
	"errors"
	"sync"
	"time"

	"PrismPanel/internal/store"
)

// ErrMCCollectorBusy 表示国际服采集器正在运行。
var ErrMCCollectorBusy = errors.New("minecraft server collector is already running")

// MCCollectionSummary 是一次国际服采集周期的结果汇总。
type MCCollectionSummary struct {
	Checked    int       `json:"checked"`
	Online     int       `json:"online"`
	Failed     int       `json:"failed"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// MCServerSeriesPoint 是单服趋势图上的一个采样点。
type MCServerSeriesPoint struct {
	SampledAt   time.Time `json:"sampled_at"`
	Value       *uint32   `json:"value,omitempty"`
	MaxPlayers  uint32    `json:"max_players"`
	LatencyMS   uint32    `json:"latency_ms"`
	VersionName string    `json:"version_name"`
}

// MCServerSeriesGame 是国际服趋势图中的一个服务器序列，供前端折线图组件直接渲染。
type MCServerSeriesGame struct {
	GameID            string                `json:"game_id"`
	Name              string                `json:"name"`
	LatestOnlineCount uint32                `json:"latest_online_count"`
	Points            []MCServerSeriesPoint `json:"points"`
}

// MCServerSeriesResponse 是国际服趋势查询响应。
type MCServerSeriesResponse struct {
	WindowStart time.Time            `json:"window_start"`
	WindowEnd   time.Time            `json:"window_end"`
	Games       []MCServerSeriesGame `json:"games"`
}

// MCServers 返回全部国际服（含最近一次 ping 状态）。
func (s *Service) MCServers(ctx context.Context) ([]store.MCServer, error) {
	return s.store.ListMCServers(ctx)
}

// MCServer 返回单个国际服。
func (s *Service) MCServer(ctx context.Context, id uint64) (store.MCServer, error) {
	return s.store.GetMCServer(ctx, id)
}

// normalizeMCServerInput 校验并规范化创建/更新国际服的输入：
// 解析 host[:port] 地址，显式传入的 Port 优先，生成规范化 ServerKey。
func (s *Service) normalizeMCServerInput(input *store.MCServerInput) error {
	host, port, err := NormalizeMinecraftAddress(input.Host)
	if err != nil {
		return err
	}
	if input.Port != 0 {
		if input.Port < 1 || input.Port > 65535 {
			return errors.New("端口必须在 1 到 65535 之间")
		}
		port = input.Port
	}
	input.Host = host
	input.Port = port
	input.ServerKey = MinecraftServerKey(host, port)
	if input.Name == "" {
		input.Name = input.ServerKey
	}
	if len([]rune(input.Name)) > 100 {
		return errors.New("服务器名称不能超过 100 个字符")
	}
	if len([]rune(input.Note)) > 500 {
		return errors.New("备注不能超过 500 个字符")
	}
	return nil
}

// CreateMCServer 创建国际服并立即做一次 ping 以填充状态。
func (s *Service) CreateMCServer(ctx context.Context, input store.MCServerInput) (store.MCServer, error) {
	if err := s.normalizeMCServerInput(&input); err != nil {
		return store.MCServer{}, err
	}
	server, err := s.store.CreateMCServer(ctx, input)
	if err != nil {
		return store.MCServer{}, err
	}
	go s.mcCollectServer(context.Background(), server.ID)
	return s.store.GetMCServer(ctx, server.ID)
}

// UpdateMCServer 更新国际服并立即做一次 ping。
func (s *Service) UpdateMCServer(ctx context.Context, id uint64, input store.MCServerInput) (store.MCServer, error) {
	if err := s.normalizeMCServerInput(&input); err != nil {
		return store.MCServer{}, err
	}
	server, err := s.store.UpdateMCServer(ctx, id, input)
	if err != nil {
		return store.MCServer{}, err
	}
	go s.mcCollectServer(context.Background(), server.ID)
	return s.store.GetMCServer(ctx, server.ID)
}

// DeleteMCServer 删除国际服（观察点级联删除）。
func (s *Service) DeleteMCServer(ctx context.Context, id uint64) error {
	return s.store.DeleteMCServer(ctx, id)
}

// MCServerSeries 查询若干国际服（按 server_key）在一段时间内的在线人数趋势。
// 返回结构直接供前端折线图组件渲染。
func (s *Service) MCServerSeries(ctx context.Context, serverKeys []string, from, to time.Time) (MCServerSeriesResponse, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		to = time.Now().UTC()
		from = to.Add(-24 * time.Hour)
	}
	all, err := s.store.ListMCServers(ctx)
	if err != nil {
		return MCServerSeriesResponse{}, err
	}
	keys := uniqueMCServerKeys(serverKeys)
	selected := make([]store.MCServer, 0, len(keys))
	if len(keys) == 0 {
		selected = all
	} else {
		byKey := make(map[string]store.MCServer, len(all))
		for _, server := range all {
			byKey[server.ServerKey] = server
		}
		for _, key := range keys {
			if server, ok := byKey[key]; ok {
				selected = append(selected, server)
			}
		}
	}
	if len(selected) > 20 {
		selected = selected[:20]
	}
	ids := make([]uint64, 0, len(selected))
	for _, server := range selected {
		ids = append(ids, server.ID)
	}
	points, err := s.store.MCServerObservationsBetweenForServers(ctx, ids, from, to)
	if err != nil {
		return MCServerSeriesResponse{}, err
	}
	games := make([]MCServerSeriesGame, 0, len(selected))
	for _, server := range selected {
		game := MCServerSeriesGame{
			GameID: server.ServerKey,
			Name:   server.Name,
			Points: make([]MCServerSeriesPoint, 0),
		}
		var latest uint32
		for _, point := range points {
			if point.ServerKey != server.ServerKey {
				continue
			}
			value := point.Online
			game.Points = append(game.Points, MCServerSeriesPoint{
				SampledAt:   point.SampledAt,
				Value:       &value,
				MaxPlayers:  point.MaxPlayers,
				LatencyMS:   point.LatencyMS,
				VersionName: point.VersionName,
			})
			latest = point.Online
		}
		game.LatestOnlineCount = latest
		games = append(games, game)
	}
	return MCServerSeriesResponse{WindowStart: from, WindowEnd: to, Games: games}, nil
}

// CollectMCNow 立即执行一次国际服采集并返回汇总。
func (s *Service) CollectMCNow(ctx context.Context) (MCCollectionSummary, error) {
	return s.mcCollectAll(ctx, "manual")
}

func uniqueMCServerKeys(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		key := MinecraftServerKeyFromAny(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}

// MinecraftServerKeyFromAny 将用户输入（host / host:port / 已规范化 key）转为规范化 key。
func MinecraftServerKeyFromAny(value string) string {
	host, port, err := NormalizeMinecraftAddress(value)
	if err != nil {
		return ""
	}
	return MinecraftServerKey(host, port)
}

// mcCollectionLoop 定时采集所有启用的国际服。
// 间隔热生效：每次 tick 后重读设置，mc_collection_interval_minutes 变化时重建 ticker，
// 修改后无需重启面板即生效（仅本函数内调整 ticker）。
func (s *Service) mcCollectionLoop(ctx context.Context) {
	interval := time.Duration(s.Settings().MinecraftCollectionIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.mcCollectAll(ctx, "scheduled"); err != nil && !errors.Is(err, ErrMCCollectorBusy) {
				s.logger.Warn("scheduled minecraft server collection", "error", err)
			}
			// 热生效：重读设置，间隔变化则重建 ticker（下次 tick 起按新间隔执行）
			if next := time.Duration(s.Settings().MinecraftCollectionIntervalMinutes) * time.Minute; next != interval {
				ticker.Reset(next)
				interval = next
			}
		}
	}
}

func (s *Service) mcCollectAll(ctx context.Context, trigger string) (MCCollectionSummary, error) {
	select {
	case s.mcMutex <- struct{}{}:
		defer func() { <-s.mcMutex }()
	default:
		return MCCollectionSummary{}, ErrMCCollectorBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	servers, err := s.store.ListEnabledMCServers(ctx)
	if err != nil {
		return MCCollectionSummary{}, err
	}
	summary := MCCollectionSummary{StartedAt: time.Now().UTC()}
	if len(servers) == 0 {
		summary.FinishedAt = time.Now().UTC()
		return summary, nil
	}
	results := make(chan mcServerResult, len(servers))
	var waitGroup sync.WaitGroup
	workers := mcPingWorkerCount
	if workers > len(servers) {
		workers = len(servers)
	}
	jobs := make(chan store.MCServer, len(servers))
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for server := range jobs {
				results <- s.mcCollectServerWithResult(server)
			}
		}()
	}
	for _, server := range servers {
		jobs <- server
	}
	close(jobs)
	waitGroup.Wait()
	close(results)
	for result := range results {
		summary.Checked++
		if result.err != nil {
			summary.Failed++
			s.logger.Warn("minecraft server ping failed", "server", result.server.ServerKey, "error", result.err)
			continue
		}
		summary.Online++
	}
	summary.FinishedAt = time.Now().UTC()
	s.logger.Info("minecraft server collection finished",
		"trigger", trigger, "checked", summary.Checked, "online", summary.Online, "failed", summary.Failed)
	go s.mcPurgeOldHistory(context.Background())
	return summary, nil
}

type mcServerResult struct {
	server store.MCServer
	err    error
}

// mcCollectServer 对单个服务器执行 ping 并写入结果（后台调用，忽略错误）。
func (s *Service) mcCollectServer(ctx context.Context, serverID uint64) {
	server, err := s.store.GetMCServer(ctx, serverID)
	if err != nil {
		return
	}
	_ = s.mcCollectServerWithResult(server)
}

// mcCollectServerWithResult 对单个服务器执行 ping、更新 last_* 字段，成功时写入观察点。
func (s *Service) mcCollectServerWithResult(server store.MCServer) mcServerResult {
	result, err := PingMCServer(context.Background(), server.Host, server.Port)
	updateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err != nil {
		_ = s.store.UpdateMCServerResult(updateCtx, server.ID, store.MCServerStatusFailed,
			nil, nil, nil, "", err.Error())
		return mcServerResult{server: server, err: err}
	}
	online := result.OnlineCount
	maximum := result.MaxPlayers
	latency := uint32(result.LatencyMS)
	if latency < 0 {
		latency = 0
	}
	if err := s.store.UpdateMCServerResult(updateCtx, server.ID, store.MCServerStatusOK,
		&online, &maximum, &latency, result.VersionName, ""); err != nil {
		return mcServerResult{server: server, err: err}
	}
	observation := store.MCServerObservation{
		ServerID:    server.ID,
		SampledAt:   time.Now().UTC(),
		Online:      online,
		MaxPlayers:  maximum,
		LatencyMS:   latency,
		VersionName: result.VersionName,
		Protocol:    result.Protocol,
	}
	if err := s.store.CreateMCServerObservation(updateCtx, observation); err != nil {
		return mcServerResult{server: server, err: err}
	}
	return mcServerResult{server: server, err: nil}
}

// mcPurgeOldHistory 按历史保留天数清理国际服观察点。
func (s *Service) mcPurgeOldHistory(ctx context.Context) {
	settings := s.Settings()
	cutoff := time.Now().UTC().Add(-time.Duration(settings.HistoryRetentionDays) * 24 * time.Hour)
	servers, err := s.store.ListMCServers(ctx)
	if err != nil {
		return
	}
	for _, server := range servers {
		if _, err := s.store.DeleteMCServerObservationsBefore(ctx, server.ID, cutoff); err != nil {
			s.logger.Debug("minecraft server history cleanup skipped", "server", server.ServerKey, "error", err)
		}
	}
}
