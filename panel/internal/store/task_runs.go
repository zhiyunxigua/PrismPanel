package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type TaskRunTarget struct {
	NodeID       string     `json:"node_id"`
	NodeName     string     `json:"node_name"`
	InstanceID   string     `json:"instance_id"`
	InstanceName string     `json:"instance_name"`
	Status       string     `json:"status"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

type TaskRun struct {
	ID               string          `json:"id"`
	ScheduledTaskID  string          `json:"scheduled_task_id"`
	TaskName         string          `json:"task_name"`
	TriggerType      string          `json:"trigger_type"`
	Status           string          `json:"status"`
	ActionType       string          `json:"action_type"`
	ActionPayload    string          `json:"action_payload,omitempty"`
	ScheduledFor     *time.Time      `json:"scheduled_for,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	TotalTargets     int             `json:"total_targets"`
	SuccessTargets   int             `json:"success_targets"`
	FailedTargets    int             `json:"failed_targets"`
	ActorUserID      string          `json:"actor_user_id,omitempty"`
	ActorUsername    string          `json:"actor_username,omitempty"`
	ActorDisplayName string          `json:"actor_display_name,omitempty"`
	Targets          []TaskRunTarget `json:"targets,omitempty"`
}

type TaskRunFilter struct {
	ScheduledTaskID string
	Status          string
	From            *time.Time
	To              *time.Time
	CursorCreatedAt *time.Time
	CursorID        string
	Limit           int
}

type TaskRunPage struct {
	Items   []TaskRun
	HasMore bool
}

func (s *Store) ClaimScheduledTask(
	ctx context.Context, taskID string, scheduledFor time.Time, nextRun *time.Time, missed bool,
) (TaskRun, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskRun{}, false, err
	}
	defer tx.Rollback()
	task, err := scanScheduledTask(tx.QueryRowContext(ctx,
		scheduledTaskSelect+" WHERE id = ? AND enabled = TRUE AND next_run_at = ? FOR UPDATE",
		taskID, scheduledFor))
	if errors.Is(err, ErrNotFound) {
		return TaskRun{}, false, nil
	}
	if err != nil {
		return TaskRun{}, false, err
	}
	task.Targets, err = scheduledTaskTargetsTx(ctx, tx, taskID)
	if err != nil {
		return TaskRun{}, false, err
	}
	now := time.Now().UTC()
	enabled := nextRun != nil
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET enabled = ?, next_run_at = ?,
		last_run_at = ?, updated_at = ? WHERE id = ?`, enabled, nextRun, scheduledFor, now, taskID); err != nil {
		return TaskRun{}, false, err
	}
	status := "queued"
	if missed {
		status = "missed"
	}
	run, err := newTaskRun(task, "scheduled", status, &scheduledFor, User{})
	if err != nil {
		return TaskRun{}, false, err
	}
	if missed {
		run.StartedAt, run.FinishedAt = &now, &now
	}
	if err := insertTaskRun(ctx, tx, run); err != nil {
		return TaskRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TaskRun{}, false, err
	}
	return run, true, nil
}

func (s *Store) CreateManualTaskRun(ctx context.Context, taskID string, actor User) (TaskRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskRun{}, err
	}
	defer tx.Rollback()
	task, err := scanScheduledTask(tx.QueryRowContext(ctx, scheduledTaskSelect+" WHERE id = ? FOR UPDATE", taskID))
	if err != nil {
		return TaskRun{}, err
	}
	task.Targets, err = scheduledTaskTargetsTx(ctx, tx, taskID)
	if err != nil {
		return TaskRun{}, err
	}
	run, err := newTaskRun(task, "manual", "queued", nil, actor)
	if err != nil {
		return TaskRun{}, err
	}
	if err := insertTaskRun(ctx, tx, run); err != nil {
		return TaskRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskRun{}, err
	}
	return run, nil
}

func newTaskRun(
	task ScheduledTask, trigger, status string, scheduledFor *time.Time, actor User,
) (TaskRun, error) {
	runID, err := newID()
	if err != nil {
		return TaskRun{}, err
	}
	targets := make([]TaskRunTarget, len(task.Targets))
	for index, target := range task.Targets {
		targetStatus := "queued"
		if status == "missed" {
			targetStatus = "missed"
		}
		targets[index] = TaskRunTarget{
			NodeID: target.NodeID, NodeName: target.NodeName, InstanceID: target.InstanceID,
			InstanceName: target.InstanceName, Status: targetStatus,
		}
	}
	return TaskRun{
		ID: runID, ScheduledTaskID: task.ID, TaskName: task.Name, TriggerType: trigger,
		Status: status, ActionType: task.ActionType, ActionPayload: task.ActionPayload,
		ScheduledFor: scheduledFor, CreatedAt: time.Now().UTC(), TotalTargets: len(targets),
		ActorUserID: actor.ID, ActorUsername: actor.Username, ActorDisplayName: actor.DisplayName,
		Targets: targets,
	}, nil
}

func insertTaskRun(ctx context.Context, tx *transaction, run TaskRun) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO task_runs
		(id, scheduled_task_id, task_name, trigger_type, status, action_type, action_payload,
		 scheduled_for, created_at, started_at, finished_at, total_targets, success_targets,
		 failed_targets, actor_user_id, actor_username, actor_display_name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID,
		run.ScheduledTaskID, run.TaskName, run.TriggerType, run.Status, run.ActionType,
		run.ActionPayload, run.ScheduledFor, run.CreatedAt, run.StartedAt, run.FinishedAt,
		run.TotalTargets, run.SuccessTargets, run.FailedTargets, run.ActorUserID,
		run.ActorUsername, run.ActorDisplayName)
	if err != nil {
		return err
	}
	for _, target := range run.Targets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_run_targets
			(run_id, node_id, node_name, instance_id, instance_name, status, started_at,
			 finished_at, error_code, error_message) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, target.NodeID, target.NodeName, target.InstanceID, target.InstanceName,
			target.Status, target.StartedAt, target.FinishedAt, target.ErrorCode, target.ErrorMessage); err != nil {
			return err
		}
	}
	return nil
}

func scheduledTaskTargetsTx(ctx context.Context, tx *transaction, taskID string) ([]ScheduledTaskTarget, error) {
	rows, err := tx.QueryContext(ctx, `SELECT node_id, node_name, server_id, instance_id,
		instance_name FROM scheduled_task_targets WHERE task_id = ? ORDER BY node_name, instance_name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ScheduledTaskTarget, 0)
	for rows.Next() {
		var target ScheduledTaskTarget
		if err := rows.Scan(&target.NodeID, &target.NodeName, &target.ServerID,
			&target.InstanceID, &target.InstanceName); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func (s *Store) StartTaskRun(ctx context.Context, runID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE task_runs SET status = 'running', started_at = ?
		WHERE id = ? AND status = 'queued'`, now, runID)
	return err
}

func (s *Store) StartTaskRunTarget(ctx context.Context, runID, nodeID, instanceID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE task_run_targets SET status = 'running', started_at = ?
		WHERE run_id = ? AND node_id = ? AND instance_id = ?`, now, runID, nodeID, instanceID)
	return err
}

func (s *Store) CompleteTaskRunTarget(
	ctx context.Context, runID, nodeID, instanceID string, success bool, errorCode, errorMessage string,
) error {
	status := "completed"
	if !success {
		status = "failed"
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE task_run_targets SET status = ?, finished_at = ?,
		error_code = ?, error_message = ? WHERE run_id = ? AND node_id = ? AND instance_id = ?`,
		status, now, errorCode, errorMessage, runID, nodeID, instanceID)
	return err
}

func (s *Store) FinishTaskRun(ctx context.Context, runID string) error {
	var total, succeeded, failed int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM task_run_targets WHERE run_id = ?`, runID).Scan(&total, &succeeded, &failed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	status := "completed"
	switch {
	case failed > 0 && succeeded > 0:
		status = "completed_with_errors"
	case failed > 0:
		status = "failed"
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `UPDATE task_runs SET status = ?, finished_at = ?,
		total_targets = ?, success_targets = ?, failed_targets = ? WHERE id = ?`,
		status, now, total, succeeded, failed, runID)
	return err
}

const taskRunSelect = `SELECT id, scheduled_task_id, task_name, trigger_type, status,
	action_type, action_payload, scheduled_for, created_at, started_at, finished_at,
	total_targets, success_targets, failed_targets, actor_user_id, actor_username,
	actor_display_name FROM task_runs`

func (s *Store) ListTaskRuns(ctx context.Context, filter TaskRunFilter) (TaskRunPage, error) {
	limit := filter.Limit
	if limit < 1 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	var query strings.Builder
	query.WriteString(taskRunSelect)
	query.WriteString(" WHERE 1 = 1")
	args := make([]any, 0, 8)
	if filter.ScheduledTaskID != "" {
		query.WriteString(" AND scheduled_task_id = ?")
		args = append(args, filter.ScheduledTaskID)
	}
	if filter.Status != "" {
		query.WriteString(" AND status = ?")
		args = append(args, filter.Status)
	}
	if filter.From != nil {
		query.WriteString(" AND created_at >= ?")
		args = append(args, *filter.From)
	}
	if filter.To != nil {
		query.WriteString(" AND created_at <= ?")
		args = append(args, *filter.To)
	}
	if filter.CursorCreatedAt != nil && filter.CursorID != "" {
		query.WriteString(" AND (created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, *filter.CursorCreatedAt, *filter.CursorCreatedAt, filter.CursorID)
	}
	query.WriteString(" ORDER BY created_at DESC, id DESC LIMIT ?")
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return TaskRunPage{}, err
	}
	defer rows.Close()
	items := make([]TaskRun, 0, limit+1)
	for rows.Next() {
		run, err := scanTaskRun(rows)
		if err != nil {
			return TaskRunPage{}, err
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		return TaskRunPage{}, err
	}
	page := TaskRunPage{Items: items, HasMore: len(items) > limit}
	if page.HasMore {
		page.Items = page.Items[:limit]
	}
	return page, nil
}

func (s *Store) GetTaskRun(ctx context.Context, id string) (TaskRun, error) {
	run, err := scanTaskRun(s.db.QueryRowContext(ctx, taskRunSelect+" WHERE id = ?", id))
	if err != nil {
		return TaskRun{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, node_name, instance_id, instance_name,
		status, started_at, finished_at, error_code, error_message FROM task_run_targets
		WHERE run_id = ? ORDER BY node_name, instance_name`, id)
	if err != nil {
		return TaskRun{}, err
	}
	defer rows.Close()
	run.Targets = make([]TaskRunTarget, 0)
	for rows.Next() {
		var target TaskRunTarget
		if err := rows.Scan(&target.NodeID, &target.NodeName, &target.InstanceID,
			&target.InstanceName, &target.Status, &target.StartedAt, &target.FinishedAt,
			&target.ErrorCode, &target.ErrorMessage); err != nil {
			return TaskRun{}, err
		}
		run.Targets = append(run.Targets, target)
	}
	return run, rows.Err()
}

func scanTaskRun(row rowScanner) (TaskRun, error) {
	var run TaskRun
	err := row.Scan(&run.ID, &run.ScheduledTaskID, &run.TaskName, &run.TriggerType,
		&run.Status, &run.ActionType, &run.ActionPayload, &run.ScheduledFor, &run.CreatedAt,
		&run.StartedAt, &run.FinishedAt, &run.TotalTargets, &run.SuccessTargets,
		&run.FailedTargets, &run.ActorUserID, &run.ActorUsername, &run.ActorDisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRun{}, ErrNotFound
	}
	return run, err
}

func (s *Store) CountRunningTaskRuns(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM task_runs WHERE status IN ('queued', 'running')",
	).Scan(&count)
	return count, err
}

func (s *Store) DeleteTaskRunsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM task_runs WHERE created_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) FailIncompleteTaskRuns(
	ctx context.Context, errorCode, errorMessage string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE task_run_targets SET status = 'failed',
		finished_at = ?, error_code = ?, error_message = ?
		WHERE status IN ('queued', 'running') AND run_id IN (
			SELECT id FROM task_runs WHERE status IN ('queued', 'running')
		)`, now, errorCode, errorMessage); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_runs SET status = 'failed', finished_at = ?,
		success_targets = (
			SELECT COUNT(*) FROM task_run_targets WHERE run_id = task_runs.id AND status = 'completed'
		),
		failed_targets = (
			SELECT COUNT(*) FROM task_run_targets WHERE run_id = task_runs.id AND status = 'failed'
		)
		WHERE status IN ('queued', 'running')`, now); err != nil {
		return err
	}
	return tx.Commit()
}
