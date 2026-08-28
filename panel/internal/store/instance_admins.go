package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) ListInstanceAdmins(ctx context.Context, nodeID, instanceID string) ([]InstanceAdmin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.user_id, u.username, u.display_name, a.assigned_at
		FROM instance_admins a JOIN users u ON u.id = a.user_id
		WHERE a.node_id = ? AND a.instance_id = ? AND u.deleted_at IS NULL AND u.status = ?
		ORDER BY u.display_name, u.username`, nodeID, instanceID, UserActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	admins := make([]InstanceAdmin, 0)
	for rows.Next() {
		var admin InstanceAdmin
		if err := rows.Scan(&admin.UserID, &admin.Username, &admin.DisplayName, &admin.AssignedAt); err != nil {
			return nil, err
		}
		admins = append(admins, admin)
	}
	return admins, rows.Err()
}

func (s *Store) IsInstanceAdmin(ctx context.Context, nodeID, instanceID, userID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instance_admins a
		JOIN users u ON u.id = a.user_id
		WHERE a.node_id = ? AND a.instance_id = ? AND a.user_id = ?
			AND u.deleted_at IS NULL AND u.status = ?`, nodeID, instanceID, userID, UserActive).Scan(&count)
	return count > 0, err
}

func (s *Store) InstanceAdminSet(ctx context.Context, userID string) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, instance_id FROM instance_admins WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var nodeID, instanceID string
		if err := rows.Scan(&nodeID, &instanceID); err != nil {
			return nil, err
		}
		result[nodeID+"\x00"+instanceID] = struct{}{}
	}
	return result, rows.Err()
}

func (s *Store) SetInstanceAdmins(ctx context.Context, nodeID, instanceID string, userIDs []string) ([]InstanceAdmin, error) {
	nodeID, instanceID = strings.TrimSpace(nodeID), strings.TrimSpace(instanceID)
	if nodeID == "" || instanceID == "" || len([]rune(nodeID)) > 32 || len([]rune(instanceID)) > 64 {
		return nil, ErrConflict
	}
	seen := make(map[string]struct{}, len(userIDs))
	ids := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		ids = append(ids, userID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, userID := range ids {
		var status string
		err = tx.QueryRowContext(ctx, "SELECT status FROM users WHERE id = ? AND deleted_at IS NULL FOR UPDATE", userID).Scan(&status)
		if errorsIsNoRows(err) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if status != UserActive {
			return nil, ErrConflict
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM instance_admins WHERE node_id = ? AND instance_id = ?", nodeID, instanceID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, userID := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO instance_admins (node_id, instance_id, user_id, assigned_at)
			VALUES (?, ?, ?, ?)`, nodeID, instanceID, userID, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListInstanceAdmins(ctx, nodeID, instanceID)
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
