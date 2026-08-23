package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

func NormalizeMinecraftUUID(value string) (string, error) {
	compact := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "")
	if len(compact) != 32 {
		return "", errors.New("UUID must contain 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(compact); err != nil {
		return "", errors.New("UUID must contain only hexadecimal characters")
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		compact[:8], compact[8:12], compact[12:16], compact[16:20], compact[20:]), nil
}

func (s *Store) OperatorState(ctx context.Context) (OperatorState, error) {
	var state OperatorState
	if err := s.db.QueryRowContext(ctx, `SELECT panel_id, revision, initialized, updated_at
		FROM operator_state WHERE id = 1`).Scan(
		&state.PanelID, &state.Revision, &state.Initialized, &state.UpdatedAt,
	); err != nil {
		return OperatorState{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT uuid, name, created_by_user_id,
		created_by_username, created_at, updated_at FROM operators ORDER BY name, uuid`)
	if err != nil {
		return OperatorState{}, err
	}
	defer rows.Close()
	state.Operators = make([]Operator, 0)
	for rows.Next() {
		var item Operator
		if err := rows.Scan(
			&item.UUID, &item.Name, &item.CreatedByUserID, &item.CreatedByUsername,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return OperatorState{}, err
		}
		state.Operators = append(state.Operators, item)
	}
	return state, rows.Err()
}

func (s *Store) PutOperator(
	ctx context.Context,
	uuid, name, actorUserID, actorUsername string,
) (OperatorState, error) {
	normalizedUUID, err := NormalizeMinecraftUUID(uuid)
	if err != nil {
		return OperatorState{}, err
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 64 {
		return OperatorState{}, errors.New("player name is too long")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OperatorState{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	var currentName string
	err = tx.QueryRowContext(ctx, `SELECT name FROM operators WHERE uuid = ? FOR UPDATE`, normalizedUUID).Scan(&currentName)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO operators
			(uuid, name, created_by_user_id, created_by_username, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			normalizedUUID, name, actorUserID, actorUsername, now, now)
	case err == nil && currentName != name:
		_, err = tx.ExecContext(ctx, `UPDATE operators SET name = ?, updated_at = ? WHERE uuid = ?`,
			name, now, normalizedUUID)
	case err == nil:
		if err = tx.Commit(); err != nil {
			return OperatorState{}, err
		}
		return s.OperatorState(ctx)
	}
	if err != nil {
		return OperatorState{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE operator_state
		SET revision = revision + 1, updated_at = ? WHERE id = 1`, now); err != nil {
		return OperatorState{}, err
	}
	if err = tx.Commit(); err != nil {
		return OperatorState{}, err
	}
	return s.OperatorState(ctx)
}

func (s *Store) DeleteOperator(ctx context.Context, uuid string) (OperatorState, error) {
	normalizedUUID, err := NormalizeMinecraftUUID(uuid)
	if err != nil {
		return OperatorState{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OperatorState{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM operators WHERE uuid = ?`, normalizedUUID)
	if err != nil {
		return OperatorState{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return OperatorState{}, err
	}
	if affected == 0 {
		return OperatorState{}, ErrNotFound
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE operator_state
		SET revision = revision + 1, updated_at = ? WHERE id = 1`, now); err != nil {
		return OperatorState{}, err
	}
	if err = tx.Commit(); err != nil {
		return OperatorState{}, err
	}
	return s.OperatorState(ctx)
}

func (s *Store) ActivateOperatorManagement(ctx context.Context) (OperatorState, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE operator_state
		SET initialized = TRUE, revision = revision + 1, updated_at = ?
		WHERE id = 1 AND initialized = FALSE`, now)
	if err != nil {
		return OperatorState{}, err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
		return OperatorState{}, affectedErr
	} else if affected == 0 {
		return s.OperatorState(ctx)
	}
	return s.OperatorState(ctx)
}
