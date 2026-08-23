package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

type ScheduledTaskTarget struct {
	NodeID       string `json:"node_id"`
	NodeName     string `json:"node_name"`
	ServerID     string `json:"server_id"`
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
}

type ScheduledTask struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	Enabled              bool                  `json:"enabled"`
	ScheduleType         string                `json:"schedule_type"`
	ScheduleConfig       json.RawMessage       `json:"schedule"`
	Timezone             string                `json:"timezone"`
	ActionType           string                `json:"action_type"`
	ActionPayload        string                `json:"action_payload,omitempty"`
	NextRunAt            *time.Time            `json:"next_run_at,omitempty"`
	LastRunAt            *time.Time            `json:"last_run_at,omitempty"`
	CreatedByUserID      string                `json:"created_by_user_id"`
	CreatedByUsername    string                `json:"created_by_username"`
	CreatedByDisplayName string                `json:"created_by_display_name"`
	CreatedAt            time.Time             `json:"created_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
	Targets              []ScheduledTaskTarget `json:"targets"`
}

func (s *Store) CreateScheduledTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error) {
	id, err := newID()
	if err != nil {
		return ScheduledTask{}, err
	}
	task.ID = id
	now := time.Now().UTC()
	task.CreatedAt, task.UpdatedAt = now, now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScheduledTask{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_tasks
		(id, name, enabled, schedule_type, schedule_config, timezone, action_type, action_payload,
		 next_run_at, last_run_at, created_by_user_id, created_by_username, created_by_display_name,
		 created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Name, task.Enabled, task.ScheduleType, task.ScheduleConfig, task.Timezone,
		task.ActionType, task.ActionPayload, task.NextRunAt, task.LastRunAt, task.CreatedByUserID,
		task.CreatedByUsername, task.CreatedByDisplayName, task.CreatedAt, task.UpdatedAt)
	if err != nil {
		return ScheduledTask{}, databaseConflict(err)
	}
	if err := replaceScheduledTaskTargets(ctx, tx, task.ID, task.Targets); err != nil {
		return ScheduledTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScheduledTask{}, err
	}
	return task, nil
}

func (s *Store) UpdateScheduledTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScheduledTask{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET name = ?, enabled = ?,
		schedule_type = ?, schedule_config = ?, timezone = ?, action_type = ?, action_payload = ?,
		next_run_at = ?, updated_at = ? WHERE id = ?`, task.Name, task.Enabled, task.ScheduleType,
		task.ScheduleConfig, task.Timezone, task.ActionType, task.ActionPayload, task.NextRunAt, now, task.ID)
	if err != nil {
		return ScheduledTask{}, databaseConflict(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ScheduledTask{}, ErrNotFound
	}
	if err := replaceScheduledTaskTargets(ctx, tx, task.ID, task.Targets); err != nil {
		return ScheduledTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScheduledTask{}, err
	}
	return s.GetScheduledTask(ctx, task.ID)
}

func (s *Store) DeleteScheduledTask(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM scheduled_tasks WHERE id = ?", id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetScheduledTask(ctx context.Context, id string) (ScheduledTask, error) {
	task, err := scanScheduledTask(s.db.QueryRowContext(ctx, scheduledTaskSelect+" WHERE id = ?", id))
	if err != nil {
		return ScheduledTask{}, err
	}
	task.Targets, err = s.scheduledTaskTargets(ctx, id)
	return task, err
}

func (s *Store) ListScheduledTasks(ctx context.Context) ([]ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx, scheduledTaskSelect+" ORDER BY created_at DESC, id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]ScheduledTask, 0)
	for rows.Next() {
		task, err := scanScheduledTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	targets, err := s.db.QueryContext(ctx, `SELECT task_id, node_id, node_name, server_id,
		instance_id, instance_name FROM scheduled_task_targets ORDER BY node_name, instance_name`)
	if err != nil {
		return nil, err
	}
	defer targets.Close()
	byID := make(map[string][]ScheduledTaskTarget)
	for targets.Next() {
		var taskID string
		var target ScheduledTaskTarget
		if err := targets.Scan(&taskID, &target.NodeID, &target.NodeName, &target.ServerID,
			&target.InstanceID, &target.InstanceName); err != nil {
			return nil, err
		}
		byID[taskID] = append(byID[taskID], target)
	}
	for index := range tasks {
		tasks[index].Targets = byID[tasks[index].ID]
	}
	return tasks, targets.Err()
}

func (s *Store) ListDueScheduledTasks(
	ctx context.Context, dueAt time.Time, limit int,
) ([]ScheduledTask, error) {
	if limit < 1 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, scheduledTaskSelect+`
		WHERE enabled = TRUE AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at, id LIMIT ?`, dueAt, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]ScheduledTask, 0)
	for rows.Next() {
		task, err := scanScheduledTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

const scheduledTaskSelect = `SELECT id, name, enabled, schedule_type, schedule_config, timezone,
	action_type, action_payload, next_run_at, last_run_at, created_by_user_id, created_by_username,
	created_by_display_name, created_at, updated_at FROM scheduled_tasks`

func scanScheduledTask(row rowScanner) (ScheduledTask, error) {
	var task ScheduledTask
	err := row.Scan(&task.ID, &task.Name, &task.Enabled, &task.ScheduleType, &task.ScheduleConfig,
		&task.Timezone, &task.ActionType, &task.ActionPayload, &task.NextRunAt, &task.LastRunAt,
		&task.CreatedByUserID, &task.CreatedByUsername, &task.CreatedByDisplayName,
		&task.CreatedAt, &task.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledTask{}, ErrNotFound
	}
	return task, err
}

func (s *Store) scheduledTaskTargets(ctx context.Context, taskID string) ([]ScheduledTaskTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, node_name, server_id, instance_id,
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

func replaceScheduledTaskTargets(ctx context.Context, tx *transaction, taskID string, targets []ScheduledTaskTarget) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM scheduled_task_targets WHERE task_id = ?", taskID); err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_task_targets
			(task_id, node_id, node_name, server_id, instance_id, instance_name)
			VALUES (?, ?, ?, ?, ?, ?)`, taskID, target.NodeID, target.NodeName, target.ServerID,
			target.InstanceID, target.InstanceName); err != nil {
			return err
		}
	}
	return nil
}

func databaseConflict(err error) error {
	if err == nil {
		return nil
	}
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
		return ErrConflict
	}
	return err
}
