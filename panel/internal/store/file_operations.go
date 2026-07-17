package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Store) CreateFileOperation(ctx context.Context, operation FileOperation) (FileOperation, error) {
	if operation.ID == "" {
		id, err := newID()
		if err != nil {
			return FileOperation{}, err
		}
		operation.ID = id
	}
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = time.Now().UTC()
	}
	if operation.Status == "" {
		operation.Status = "pending"
	}
	detail, err := json.Marshal(operation.Detail)
	if err != nil {
		return FileOperation{}, fmt.Errorf("encode file operation detail: %w", err)
	}
	var actorID any
	if operation.ActorUserID != "" {
		actorID = operation.ActorUserID
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO file_operations
		(id, request_id, created_at, expires_at, actor_user_id, session_id, actor_username,
		 actor_display_name, source_ip, user_agent, action, node_id, resource_type,
		 resource_id, status, error_code, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		operation.ID, operation.RequestID, operation.CreatedAt, operation.ExpiresAt, actorID,
		operation.SessionID, operation.ActorUsername, operation.ActorDisplayName,
		operation.SourceIP, operation.UserAgent, operation.Action, operation.NodeID,
		operation.ResourceType, operation.ResourceID, operation.Status, operation.ErrorCode, detail)
	if err != nil {
		return FileOperation{}, err
	}
	return operation, nil
}

func (s *Store) CompleteFileOperation(ctx context.Context, id, nodeID string, success bool, errorCode string) (FileOperation, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FileOperation{}, false, err
	}
	defer tx.Rollback()
	operation, err := readFileOperation(tx.QueryRowContext(ctx, `SELECT id, request_id, created_at,
		expires_at, completed_at, actor_user_id, session_id, actor_username, actor_display_name,
		source_ip, user_agent, action, node_id, resource_type, resource_id, status, error_code, detail
		FROM file_operations WHERE id = ? AND node_id = ? FOR UPDATE`, id, nodeID))
	if err != nil {
		if err == sql.ErrNoRows {
			return FileOperation{}, false, nil
		}
		return FileOperation{}, false, err
	}
	if operation.Status != "pending" {
		return operation, false, nil
	}
	status := "succeeded"
	if !success {
		status = "failed"
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE file_operations
		SET status = ?, error_code = ?, completed_at = ? WHERE id = ?`, status, errorCode, now, id); err != nil {
		return FileOperation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return FileOperation{}, false, err
	}
	operation.Status, operation.ErrorCode, operation.CompletedAt = status, errorCode, &now
	return operation, true, nil
}

func (s *Store) ExpireFileOperations(ctx context.Context, limit int) ([]FileOperation, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_id FROM file_operations
		WHERE status = 'pending' AND expires_at < ? ORDER BY expires_at LIMIT ?`, time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	type expiredID struct{ id, nodeID string }
	ids := make([]expiredID, 0)
	for rows.Next() {
		var item expiredID
		if err := rows.Scan(&item.id, &item.nodeID); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]FileOperation, 0, len(ids))
	for _, item := range ids {
		operation, changed, err := s.CompleteFileOperation(ctx, item.id, item.nodeID, false, "TICKET_EXPIRED")
		if err != nil {
			return nil, err
		}
		if changed {
			result = append(result, operation)
		}
	}
	return result, nil
}

type fileOperationRow interface {
	Scan(...any) error
}

func readFileOperation(row fileOperationRow) (FileOperation, error) {
	var operation FileOperation
	var actorID sql.NullString
	var completedAt sql.NullTime
	var detail []byte
	err := row.Scan(&operation.ID, &operation.RequestID, &operation.CreatedAt, &operation.ExpiresAt,
		&completedAt, &actorID, &operation.SessionID, &operation.ActorUsername,
		&operation.ActorDisplayName, &operation.SourceIP, &operation.UserAgent, &operation.Action,
		&operation.NodeID, &operation.ResourceType, &operation.ResourceID, &operation.Status,
		&operation.ErrorCode, &detail)
	if actorID.Valid {
		operation.ActorUserID = actorID.String
	}
	if completedAt.Valid {
		operation.CompletedAt = &completedAt.Time
	}
	if len(detail) > 0 {
		_ = json.Unmarshal(detail, &operation.Detail)
	}
	return operation, err
}
