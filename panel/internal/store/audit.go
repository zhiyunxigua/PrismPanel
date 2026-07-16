package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateAudit(ctx context.Context, entry AuditLog) error {
	id, err := newID()
	if err != nil {
		return err
	}
	detail, err := json.Marshal(entry.Detail)
	if err != nil {
		return fmt.Errorf("encode audit detail: %w", err)
	}
	var actorID any
	if entry.ActorUserID != "" {
		actorID = entry.ActorUserID
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_logs
		(id, request_id, created_at, actor_user_id, session_id, actor_username, actor_display_name,
		 source_ip, user_agent, action, resource_type, resource_id, resource_name,
		 risk_level, success, error_code, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, entry.RequestID, time.Now().UTC(), actorID, entry.SessionID, entry.ActorUsername, entry.ActorDisplayName,
		entry.SourceIP, entry.UserAgent, entry.Action, entry.ResourceType, entry.ResourceID,
		entry.ResourceName, entry.RiskLevel, entry.Success, entry.ErrorCode, detail)
	return err
}

func (s *Store) ListAudit(ctx context.Context, search, action string, success *bool, page, pageSize int) (AuditList, error) {
	conditions := []string{"1 = 1"}
	args := make([]any, 0, 6)
	if search != "" {
		conditions = append(conditions, "(actor_username LIKE ? OR actor_display_name LIKE ? OR resource_name LIKE ?)")
		value := "%" + search + "%"
		args = append(args, value, value, value)
	}
	if action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, action)
	}
	if success != nil {
		conditions = append(conditions, "success = ?")
		args = append(args, *success)
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs WHERE "+where, args...).Scan(&total); err != nil {
		return AuditList{}, err
	}
	queryArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	rows, err := s.db.QueryContext(ctx, `SELECT id, request_id, created_at, actor_user_id, session_id,
		actor_username, actor_display_name, source_ip, user_agent, action, resource_type,
		resource_id, resource_name, risk_level, success, error_code, detail
		FROM audit_logs WHERE `+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return AuditList{}, err
	}
	defer rows.Close()
	items := make([]AuditLog, 0)
	for rows.Next() {
		var item AuditLog
		var actorID sql.NullString
		var detail []byte
		if err := rows.Scan(&item.ID, &item.RequestID, &item.CreatedAt, &actorID, &item.SessionID,
			&item.ActorUsername, &item.ActorDisplayName, &item.SourceIP, &item.UserAgent,
			&item.Action, &item.ResourceType, &item.ResourceID, &item.ResourceName,
			&item.RiskLevel, &item.Success, &item.ErrorCode, &detail); err != nil {
			return AuditList{}, err
		}
		if actorID.Valid {
			item.ActorUserID = actorID.String
		}
		_ = json.Unmarshal(detail, &item.Detail)
		items = append(items, item)
	}
	return AuditList{Items: items, Total: total, Page: page, PageSize: pageSize}, rows.Err()
}
