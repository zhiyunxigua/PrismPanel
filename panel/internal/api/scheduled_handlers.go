package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PrismPanel/internal/schedule"
	"PrismPanel/internal/store"
)

type scheduledTaskRequest struct {
	Name          string                      `json:"name"`
	Enabled       bool                        `json:"enabled"`
	ScheduleType  string                      `json:"schedule_type"`
	Schedule      json.RawMessage             `json:"schedule"`
	Timezone      string                      `json:"timezone"`
	ActionType    string                      `json:"action_type"`
	ActionPayload string                      `json:"action_payload"`
	Targets       []store.ScheduledTaskTarget `json:"targets"`
}

type taskRunCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func (s *Server) handleScheduledTasks(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "schedule.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		tasks, err := s.store.ListScheduledTasks(request.Context())
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeSuccess(writer, map[string]any{"items": tasks})
	case http.MethodPost:
		if err := s.authorize(request, "schedule.manage"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.createScheduledTask(writer, request)
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Server) handleScheduledTask(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/scheduled-tasks/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	taskID := parts[0]
	if len(parts) == 2 {
		if parts[1] != "run" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if err := s.authorize(request, "schedule.manage"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.runScheduledTask(writer, request, taskID)
		return
	}
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "schedule.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		task, err := s.store.GetScheduledTask(request.Context(), taskID)
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeSuccess(writer, task)
	case http.MethodPut:
		if err := s.authorize(request, "schedule.manage"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.updateScheduledTask(writer, request, taskID)
	case http.MethodDelete:
		if err := s.authorize(request, "schedule.manage"); err != nil {
			writeRequestError(writer, err)
			return
		}
		err := s.store.DeleteScheduledTask(request.Context(), taskID)
		err = publicError(err)
		s.record(request, "schedule.delete", taskID, nil, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	default:
		methodNotAllowed(writer, "GET, PUT, DELETE")
	}
}

func (s *Server) createScheduledTask(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeScheduledTaskInput(request)
	var task store.ScheduledTask
	if err == nil {
		err = s.authorizeScheduledAction(request, input.ActionType)
	}
	if err == nil {
		task, err = s.scheduler.CreateTask(request.Context(), input, currentSession(request).User)
	}
	err = publicScheduledError(err)
	s.record(request, "schedule.create", task.ID, map[string]any{
		"name": input.Name, "action_type": input.ActionType, "target_count": len(input.Targets),
	}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, response{Success: true, Data: task})
}

func (s *Server) updateScheduledTask(writer http.ResponseWriter, request *http.Request, taskID string) {
	input, err := decodeScheduledTaskInput(request)
	var task store.ScheduledTask
	if err == nil {
		err = s.authorizeScheduledAction(request, input.ActionType)
	}
	if err == nil {
		task, err = s.scheduler.UpdateTask(request.Context(), taskID, input)
	}
	err = publicScheduledError(err)
	s.record(request, "schedule.update", taskID, map[string]any{
		"name": input.Name, "action_type": input.ActionType, "target_count": len(input.Targets),
	}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, task)
}

func (s *Server) runScheduledTask(writer http.ResponseWriter, request *http.Request, taskID string) {
	task, err := s.store.GetScheduledTask(request.Context(), taskID)
	if err == nil {
		err = s.authorizeScheduledAction(request, task.ActionType)
	}
	var run store.TaskRun
	if err == nil {
		run, err = s.scheduler.RunNow(request.Context(), taskID, currentSession(request).User)
	}
	err = publicScheduledError(err)
	s.record(request, "schedule.run", taskID, nil, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, response{Success: true, Data: run})
}

func decodeScheduledTaskInput(request *http.Request) (schedule.TaskInput, error) {
	body, err := readBody(request)
	if err != nil {
		return schedule.TaskInput{}, err
	}
	var input scheduledTaskRequest
	if err := json.Unmarshal(body, &input); err != nil {
		return schedule.TaskInput{}, apiError("INVALID_REQUEST", "定时任务请求格式无效")
	}
	return schedule.TaskInput{
		Name: input.Name, Enabled: input.Enabled, ScheduleType: input.ScheduleType,
		Schedule: input.Schedule, Timezone: input.Timezone, ActionType: input.ActionType,
		ActionPayload: input.ActionPayload, Targets: input.Targets,
	}, nil
}

func publicScheduledError(err error) error {
	if err == nil {
		return nil
	}
	var validation *schedule.ValidationError
	if errors.As(err, &validation) {
		return apiError("INVALID_REQUEST", validation.Message)
	}
	return publicError(err)
}

func (s *Server) authorizeScheduledAction(request *http.Request, action string) error {
	permission := schedule.ActionPermission(action)
	if permission == "" {
		return apiError("INVALID_REQUEST", "任务动作无效")
	}
	return s.authorize(request, permission)
}

func (s *Server) handleTaskRuns(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	if err := s.authorize(request, "task.view"); err != nil {
		writeRequestError(writer, err)
		return
	}
	filter, err := parseTaskRunFilter(request)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	page, err := s.store.ListTaskRuns(request.Context(), filter)
	if err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	running, err := s.store.CountRunningTaskRuns(request.Context())
	if err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	cursor := ""
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		cursor = encodeTaskRunCursor(taskRunCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	writeSuccess(writer, map[string]any{
		"items": page.Items, "has_more": page.HasMore, "next_cursor": cursor,
		"running_count": running,
	})
}

func (s *Server) handleTaskRun(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	if err := s.authorize(request, "task.view"); err != nil {
		writeRequestError(writer, err)
		return
	}
	id := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/task-runs/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(writer, request)
		return
	}
	run, err := s.store.GetTaskRun(request.Context(), id)
	if err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	writeSuccess(writer, run)
}

func parseTaskRunFilter(request *http.Request) (store.TaskRunFilter, error) {
	query := request.URL.Query()
	filter := store.TaskRunFilter{
		ScheduledTaskID: strings.TrimSpace(query.Get("scheduled_task_id")),
		Status:          strings.TrimSpace(query.Get("status")),
		Limit:           30,
	}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return store.TaskRunFilter{}, apiError("INVALID_REQUEST", "日志条数无效")
		}
		filter.Limit = limit
	}
	switch filter.Status {
	case "", "queued", "running", "completed", "completed_with_errors", "failed", "missed":
	default:
		return store.TaskRunFilter{}, apiError("INVALID_REQUEST", "日志状态无效")
	}
	var err error
	if filter.From, err = parseTaskRunTime(query.Get("from")); err != nil {
		return store.TaskRunFilter{}, apiError("INVALID_REQUEST", "开始时间无效")
	}
	if filter.To, err = parseTaskRunTime(query.Get("to")); err != nil {
		return store.TaskRunFilter{}, apiError("INVALID_REQUEST", "结束时间无效")
	}
	if cursor := strings.TrimSpace(query.Get("cursor")); cursor != "" {
		var value taskRunCursor
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(decoded, &value) != nil ||
			value.ID == "" || value.CreatedAt.IsZero() {
			return store.TaskRunFilter{}, apiError("INVALID_REQUEST", "日志游标无效")
		}
		filter.CursorCreatedAt, filter.CursorID = &value.CreatedAt, value.ID
	}
	return filter, nil
}

func parseTaskRunTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func encodeTaskRunCursor(value taskRunCursor) string {
	encoded, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(encoded)
}
