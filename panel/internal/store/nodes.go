package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
)

const nodeColumns = `id, daemon_id, name, base_url, public_url, token_ciphertext, enabled,
	daemon_version, protocol_version, capabilities, last_connected_at, last_error, created_at, updated_at`

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+nodeColumns+" FROM nodes ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Node, 0)
	for rows.Next() {
		item, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetNode(ctx context.Context, id string) (Node, error) {
	return scanNode(s.db.QueryRowContext(ctx, "SELECT "+nodeColumns+" FROM nodes WHERE id = ?", id))
}

func (s *Store) CreateNode(ctx context.Context, node Node) (Node, error) {
	now := time.Now().UTC()
	capabilities, _ := json.Marshal(node.Capabilities)
	_, err := s.db.ExecContext(ctx, `INSERT INTO nodes
		(id, daemon_id, name, base_url, public_url, token_ciphertext, enabled,
		 daemon_version, protocol_version, capabilities, last_connected_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.ID, node.DaemonID, node.Name, node.BaseURL, node.PublicURL, node.TokenCiphertext,
		node.Enabled, node.DaemonVersion, node.ProtocolVersion, capabilities,
		node.LastConnectedAt, node.LastError, now, now)
	if err != nil {
		return Node{}, normalizeNodeError(err)
	}
	node.CreatedAt, node.UpdatedAt = now, now
	return node, nil
}

func (s *Store) UpdateNode(ctx context.Context, node Node) (Node, error) {
	now := time.Now().UTC()
	capabilities, _ := json.Marshal(node.Capabilities)
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET daemon_id = ?, name = ?, base_url = ?,
		public_url = ?, token_ciphertext = ?, enabled = ?, daemon_version = ?,
		protocol_version = ?, capabilities = ?, last_connected_at = ?, last_error = ?, updated_at = ?
		WHERE id = ?`, node.DaemonID, node.Name, node.BaseURL, node.PublicURL,
		node.TokenCiphertext, node.Enabled, node.DaemonVersion, node.ProtocolVersion,
		capabilities, node.LastConnectedAt, node.LastError, now, node.ID)
	if err != nil {
		return Node{}, normalizeNodeError(err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Node{}, ErrNotFound
	}
	node.UpdatedAt = now
	return node, nil
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM nodes WHERE id = ?", id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateNodeRuntime(ctx context.Context, id, daemonID, daemonVersion, protocolVersion string,
	capabilities []string, connectedAt *time.Time, lastError string) error {
	encoded, _ := json.Marshal(capabilities)
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET
		daemon_id = CASE WHEN ? = '' THEN daemon_id ELSE ? END,
		daemon_version = ?, protocol_version = ?,
		capabilities = ?, last_connected_at = COALESCE(?, last_connected_at), last_error = ?, updated_at = ?
		WHERE id = ?`, daemonID, daemonID, daemonVersion, protocolVersion, encoded,
		connectedAt, lastError, time.Now().UTC(), id)
	return err
}

type nodeScanner interface {
	Scan(...any) error
}

func scanNode(scanner nodeScanner) (Node, error) {
	var node Node
	var capabilities []byte
	err := scanner.Scan(&node.ID, &node.DaemonID, &node.Name, &node.BaseURL, &node.PublicURL,
		&node.TokenCiphertext, &node.Enabled, &node.DaemonVersion, &node.ProtocolVersion,
		&capabilities, &node.LastConnectedAt, &node.LastError, &node.CreatedAt, &node.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, err
	}
	if len(capabilities) > 0 {
		_ = json.Unmarshal(capabilities, &node.Capabilities)
	}
	if node.Capabilities == nil {
		node.Capabilities = []string{}
	}
	return node, nil
}

func normalizeNodeError(err error) error {
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
		return ErrConflict
	}
	return fmt.Errorf("save node: %w", err)
}
