package netgames

import (
	"context"
	"log/slog"

	"PrismPanel/internal/store"
)

// Service 提供国际版（Minecraft Java 版服务器监控）相关能力：
// 服务器增删改查、状态采集与趋势查询。
type Service struct {
	store   *store.Store
	state   *StateStore
	logger  *slog.Logger
	mcMutex chan struct{}
}

func NewService(repository *store.Store, baseDir string, logger *slog.Logger) (*Service, error) {
	state, err := NewStateStore(baseDir)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store: repository, state: state, logger: logger,
		mcMutex: make(chan struct{}, 1),
	}, nil
}

// Start 启动国际服定时采集循环。
func (s *Service) Start(ctx context.Context) {
	go s.mcCollectionLoop(ctx)
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
