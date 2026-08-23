package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type UserPreference struct {
	UserID        string          `json:"user_id"`
	Namespace     string          `json:"namespace"`
	SchemaVersion uint16          `json:"schema_version"`
	Settings      json.RawMessage `json:"settings"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (s *Store) GetUserPreference(ctx context.Context, userID, namespace string) (UserPreference, error) {
	var preference UserPreference
	err := s.db.QueryRowContext(ctx, `SELECT user_id, namespace, schema_version, settings,
		created_at, updated_at FROM user_preferences WHERE user_id = ? AND namespace = ?`,
		userID, namespace,
	).Scan(&preference.UserID, &preference.Namespace, &preference.SchemaVersion,
		&preference.Settings, &preference.CreatedAt, &preference.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserPreference{}, ErrNotFound
	}
	return preference, err
}

func (s *Store) UpsertUserPreference(
	ctx context.Context, userID, namespace string, schemaVersion uint16, settings json.RawMessage,
) (UserPreference, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_preferences
		(user_id, namespace, schema_version, settings, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE schema_version = VALUES(schema_version),
		settings = VALUES(settings), updated_at = VALUES(updated_at)`,
		userID, namespace, schemaVersion, settings, now, now,
	)
	if err != nil {
		return UserPreference{}, err
	}
	return s.GetUserPreference(ctx, userID, namespace)
}
