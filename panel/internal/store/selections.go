package store

import (
	"context"
	"fmt"
	"time"
)

type TargetRule struct {
	NodeID   string `json:"node_id"`
	ServerID string `json:"server_id,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type ProxySyncOwner struct {
	NodeID   string `json:"node_id"`
	ServerID string `json:"server_id"`
}

func (s *Store) ProxySyncRules(ctx context.Context, nodeID, serverID string) ([]TargetRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target_node_id, target_server_id, enabled
		FROM proxy_sync_rules WHERE proxy_node_id = ? AND proxy_server_id = ?
		ORDER BY target_node_id, target_server_id`, nodeID, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargetRules(rows)
}

func (s *Store) ReplaceProxySyncRules(
	ctx context.Context,
	nodeID, serverID string,
	rules []TargetRule,
) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO proxy_sync_owners
		(proxy_node_id, proxy_server_id, updated_at) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at)`,
		nodeID, serverID, time.Now().UTC()); err != nil {
		return err
	}
	return s.replaceTargetRules(ctx,
		"DELETE FROM proxy_sync_rules WHERE proxy_node_id = ? AND proxy_server_id = ?",
		`INSERT INTO proxy_sync_rules
			(proxy_node_id, proxy_server_id, target_node_id, target_server_id, enabled, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		[]any{nodeID, serverID}, rules,
	)
}

func (s *Store) ProxySyncOwners(ctx context.Context) ([]ProxySyncOwner, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT proxy_node_id, proxy_server_id
		FROM proxy_sync_owners ORDER BY proxy_node_id, proxy_server_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProxySyncOwner, 0)
	for rows.Next() {
		var item ProxySyncOwner
		if err := rows.Scan(&item.NodeID, &item.ServerID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DeleteProxySyncOwner(ctx context.Context, nodeID, serverID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM proxy_sync_rules WHERE proxy_node_id = ? AND proxy_server_id = ?",
		nodeID, serverID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM proxy_sync_owners WHERE proxy_node_id = ? AND proxy_server_id = ?",
		nodeID, serverID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteProxySyncTarget(ctx context.Context, nodeID, serverID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM proxy_sync_rules WHERE target_node_id = ? AND target_server_id = ?",
		nodeID, serverID,
	)
	return err
}

func (s *Store) DeleteSelectionNode(ctx context.Context, nodeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM proxy_sync_rules WHERE proxy_node_id = ? OR target_node_id = ?",
		nodeID, nodeID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM proxy_sync_owners WHERE proxy_node_id = ?",
		nodeID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM plugin_deploy_preferences WHERE target_node_id = ?",
		nodeID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PluginDeployPreferences(
	ctx context.Context,
	pluginType, pluginID string,
) ([]TargetRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target_node_id, target_server_id, enabled
		FROM plugin_deploy_preferences WHERE plugin_type = ? AND plugin_id = ?
		ORDER BY target_node_id, target_server_id`, pluginType, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTargetRules(rows)
}

// RemovePluginDeployPreferences 删除某个仓库条目（插件/模组）的全部部署偏好行。
// 条目级删除或最后制品删除（条目整体移除）时调用，避免重传同名插件时旧规则复活、
// confirmDeployedDelete 误报「存在部署记录」。
func (s *Store) RemovePluginDeployPreferences(ctx context.Context, pluginType, pluginID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM plugin_deploy_preferences WHERE plugin_type = ? AND plugin_id = ?",
		pluginType, pluginID,
	)
	return err
}

func (s *Store) ReplacePluginDeployPreferences(
	ctx context.Context,
	pluginType, pluginID string,
	rules []TargetRule,
) error {
	return s.replaceTargetRules(ctx,
		"DELETE FROM plugin_deploy_preferences WHERE plugin_type = ? AND plugin_id = ?",
		`INSERT INTO plugin_deploy_preferences
			(plugin_type, plugin_id, target_node_id, target_server_id, enabled, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		[]any{pluginType, pluginID}, rules,
	)
}

func (s *Store) replaceTargetRules(
	ctx context.Context,
	deleteStatement, insertStatement string,
	owner []any,
	rules []TargetRule,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, deleteStatement, owner...); err != nil {
		return err
	}
	now := time.Now().UTC()
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if rule.NodeID == "" {
			return fmt.Errorf("target node id is required")
		}
		key := rule.NodeID + "\x00" + rule.ServerID
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate target rule")
		}
		seen[key] = struct{}{}
		arguments := append(append([]any{}, owner...), rule.NodeID, rule.ServerID, rule.Enabled, now)
		if _, err := tx.ExecContext(ctx, insertStatement, arguments...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ruleRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanTargetRules(rows ruleRows) ([]TargetRule, error) {
	result := make([]TargetRule, 0)
	for rows.Next() {
		var item TargetRule
		if err := rows.Scan(&item.NodeID, &item.ServerID, &item.Enabled); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
