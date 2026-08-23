package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"PrismPanel/internal/consolecommand"
	"PrismPanel/internal/daemon"
	"PrismPanel/internal/store"
)

const (
	maxTargets        = 200
	maxNameRunes      = 100
	maxCommandRunes   = 2000
	executionTimeout  = 2 * time.Minute
	missedGracePeriod = 30 * time.Second
	logRetention      = 30 * 24 * time.Hour
)

type TaskInput struct {
	Name          string
	Enabled       bool
	ScheduleType  string
	Schedule      json.RawMessage
	Timezone      string
	ActionType    string
	ActionPayload string
	Targets       []store.ScheduledTaskTarget
}

type Service struct {
	store           *store.Store
	connections     *daemon.Manager
	manageOperators bool
	logger          *slog.Logger

	mu  sync.RWMutex
	ctx context.Context
}

func NewService(
	repository *store.Store,
	connections *daemon.Manager,
	manageOperators bool,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store: repository, connections: connections, manageOperators: manageOperators, logger: logger,
		ctx: context.Background(),
	}
}

func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	recoveryContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := s.store.FailIncompleteTaskRuns(
		recoveryContext, "PANEL_RESTARTED", "Panel 重启前任务未执行完成",
	); err != nil {
		s.logger.Error("recover incomplete scheduled task runs", "error", err)
	}
	cancel()
	go s.scheduleLoop(ctx)
	go s.cleanupLoop(ctx)
}

func (s *Service) CreateTask(
	ctx context.Context, input TaskInput, actor store.User,
) (store.ScheduledTask, error) {
	task, err := s.prepareTask(ctx, input, time.Now().UTC())
	if err != nil {
		return store.ScheduledTask{}, err
	}
	task.CreatedByUserID = actor.ID
	task.CreatedByUsername = actor.Username
	task.CreatedByDisplayName = actor.DisplayName
	return s.store.CreateScheduledTask(ctx, task)
}

func (s *Service) UpdateTask(
	ctx context.Context, id string, input TaskInput,
) (store.ScheduledTask, error) {
	existing, err := s.store.GetScheduledTask(ctx, id)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	task, err := s.prepareTask(ctx, input, time.Now().UTC())
	if err != nil {
		return store.ScheduledTask{}, err
	}
	task.ID = existing.ID
	task.LastRunAt = existing.LastRunAt
	task.CreatedByUserID = existing.CreatedByUserID
	task.CreatedByUsername = existing.CreatedByUsername
	task.CreatedByDisplayName = existing.CreatedByDisplayName
	return s.store.UpdateScheduledTask(ctx, task)
}

func (s *Service) RunNow(
	ctx context.Context, taskID string, actor store.User,
) (store.TaskRun, error) {
	run, err := s.store.CreateManualTaskRun(ctx, taskID, actor)
	if err != nil {
		return store.TaskRun{}, err
	}
	go s.execute(s.serviceContext(), run)
	return run, nil
}

func ActionPermission(action string) string {
	switch action {
	case "start":
		return "instance.start"
	case "stop":
		return "instance.stop"
	case "restart":
		return "instance.restart"
	case "kill":
		return "instance.kill"
	case "command":
		return "console.command"
	default:
		return ""
	}
}

func (s *Service) prepareTask(
	ctx context.Context, input TaskInput, now time.Time,
) (store.ScheduledTask, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes {
		return store.ScheduledTask{}, &ValidationError{Message: "任务名称不能为空且不能超过 100 个字符"}
	}
	timezone, _, err := NormalizeTimezone(input.Timezone)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	scheduleConfig, err := NormalizeConfig(input.ScheduleType, input.Schedule)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	nextRunAt, err := FirstRun(input.ScheduleType, scheduleConfig, timezone, now)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	if !input.Enabled {
		nextRunAt = nil
	}
	action := strings.TrimSpace(input.ActionType)
	if ActionPermission(action) == "" {
		return store.ScheduledTask{}, &ValidationError{Message: "任务动作无效"}
	}
	payload := strings.TrimSpace(input.ActionPayload)
	if action == "command" {
		if payload == "" || utf8.RuneCountInString(payload) > maxCommandRunes {
			return store.ScheduledTask{}, &ValidationError{Message: "控制台命令不能为空且不能超过 2000 个字符"}
		}
		if errors.Is(consolecommand.Validate(payload, s.manageOperators), consolecommand.ErrOperatorManagement) {
			return store.ScheduledTask{}, &ValidationError{Message: "当前配置由面板接管游戏内 OP，不能发送 op 或 deop 命令"}
		}
	} else {
		payload = ""
	}
	targets, err := s.normalizeTargets(ctx, input.Targets)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	return store.ScheduledTask{
		Name: name, Enabled: input.Enabled, ScheduleType: input.ScheduleType,
		ScheduleConfig: scheduleConfig, Timezone: timezone, ActionType: action,
		ActionPayload: payload, NextRunAt: nextRunAt, Targets: targets,
	}, nil
}

func (s *Service) normalizeTargets(
	ctx context.Context, targets []store.ScheduledTaskTarget,
) ([]store.ScheduledTaskTarget, error) {
	if len(targets) == 0 || len(targets) > maxTargets {
		return nil, &ValidationError{Message: "任务目标数量必须在 1 到 200 之间"}
	}
	nodes := make(map[string]store.Node)
	seen := make(map[string]struct{}, len(targets))
	result := make([]store.ScheduledTaskTarget, 0, len(targets))
	for _, item := range targets {
		item.NodeID = strings.TrimSpace(item.NodeID)
		item.ServerID = strings.TrimSpace(item.ServerID)
		item.InstanceID = strings.TrimSpace(item.InstanceID)
		item.InstanceName = strings.TrimSpace(item.InstanceName)
		if item.NodeID == "" || item.ServerID == "" || item.InstanceID == "" {
			return nil, &ValidationError{Message: "任务目标信息不完整"}
		}
		key := item.NodeID + "\x00" + item.InstanceID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		node, exists := nodes[item.NodeID]
		if !exists {
			var err error
			node, err = s.store.GetNode(ctx, item.NodeID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil, &ValidationError{Message: "任务目标节点不存在"}
				}
				return nil, err
			}
			nodes[item.NodeID] = node
		}
		item.NodeName = node.Name
		if item.InstanceName == "" {
			item.InstanceName = item.InstanceID
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	s.processDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processDue(ctx)
		}
	}
}

func (s *Service) processDue(ctx context.Context) {
	now := time.Now().UTC()
	cycleContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tasks, err := s.store.ListDueScheduledTasks(cycleContext, now, 100)
	if err != nil {
		s.logger.Error("list due scheduled tasks", "error", err)
		return
	}
	for _, task := range tasks {
		if task.NextRunAt == nil {
			continue
		}
		scheduledFor := task.NextRunAt.UTC()
		nextRun, nextErr := NextAfter(
			task.ScheduleType, task.ScheduleConfig, task.Timezone, scheduledFor, now,
		)
		missed := now.Sub(scheduledFor) > missedGracePeriod
		if nextErr != nil {
			s.logger.Error("calculate next scheduled task run", "task_id", task.ID, "error", nextErr)
			nextRun = nil
			missed = true
		}
		run, claimed, err := s.store.ClaimScheduledTask(
			cycleContext, task.ID, scheduledFor, nextRun, missed,
		)
		if err != nil {
			s.logger.Error("claim scheduled task", "task_id", task.ID, "error", err)
			continue
		}
		if claimed && !missed {
			go s.execute(s.serviceContext(), run)
		}
	}
}

func (s *Service) execute(ctx context.Context, run store.TaskRun) {
	if err := s.store.StartTaskRun(ctx, run.ID); err != nil {
		s.logger.Error("start scheduled task run", "run_id", run.ID, "error", err)
		return
	}
	var wait sync.WaitGroup
	for _, target := range run.Targets {
		target := target
		wait.Add(1)
		go func() {
			defer wait.Done()
			s.executeTarget(ctx, run, target)
		}()
	}
	wait.Wait()
	finishContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.store.FinishTaskRun(finishContext, run.ID); err != nil {
		s.logger.Error("finish scheduled task run", "run_id", run.ID, "error", err)
	}
}

func (s *Service) executeTarget(
	ctx context.Context, run store.TaskRun, target store.TaskRunTarget,
) {
	if err := s.store.StartTaskRunTarget(ctx, run.ID, target.NodeID, target.InstanceID); err != nil {
		s.logger.Error("start scheduled task target", "run_id", run.ID, "error", err)
		return
	}
	callContext, cancel := context.WithTimeout(ctx, executionTimeout)
	defer cancel()
	input := map[string]any{"instance_id": target.InstanceID}
	messageType := "instance." + run.ActionType
	if run.ActionType == "command" {
		messageType = "console.command"
		input["command"] = run.ActionPayload
	}
	var err error
	if run.ActionType == "command" &&
		errors.Is(
			consolecommand.Validate(run.ActionPayload, s.manageOperators),
			consolecommand.ErrOperatorManagement,
		) {
		err = &daemon.APIError{
			Code:    "COMMAND_FORBIDDEN",
			Message: "当前配置由面板接管游戏内 OP，不能发送 op 或 deop 命令",
		}
	} else {
		err = s.connections.Call(callContext, target.NodeID, messageType, input, nil)
	}
	code, message := taskError(err)
	completeContext, completeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer completeCancel()
	if completeErr := s.store.CompleteTaskRunTarget(
		completeContext, run.ID, target.NodeID, target.InstanceID, err == nil, code, message,
	); completeErr != nil {
		s.logger.Error("complete scheduled task target", "run_id", run.ID, "error", completeErr)
	}
}

func taskError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	code := "INTERNAL"
	var daemonError *daemon.APIError
	switch {
	case errors.As(err, &daemonError):
		code = daemonError.Code
	case errors.Is(err, daemon.ErrDisconnected):
		code = "DAEMON_UNAVAILABLE"
	case errors.Is(err, context.DeadlineExceeded):
		code = "TIMEOUT"
	case errors.Is(err, context.Canceled):
		code = "CANCELED"
	}
	message := []rune(err.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	return code, string(message)
}

func (s *Service) cleanupLoop(ctx context.Context) {
	s.cleanup(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanup(ctx)
		}
	}
}

func (s *Service) cleanup(ctx context.Context) {
	cleanupContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deleted, err := s.store.DeleteTaskRunsBefore(
		cleanupContext, time.Now().UTC().Add(-logRetention),
	)
	if err != nil {
		s.logger.Error("clean scheduled task runs", "error", err)
		return
	}
	if deleted > 0 {
		s.logger.Info("cleaned scheduled task runs", "deleted", deleted)
	}
}

func (s *Service) serviceContext() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctx
}
