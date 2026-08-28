package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

func (s *Store) CreateAPIKey(ctx context.Context, userID string, tokenHash []byte, prefix string, now time.Time) (APIKey, error) {
	return s.saveAPIKey(ctx, userID, tokenHash, prefix, now, false)
}

func (s *Store) RotateAPIKey(ctx context.Context, userID string, tokenHash []byte, prefix string, now time.Time) (APIKey, error) {
	return s.saveAPIKey(ctx, userID, tokenHash, prefix, now, true)
}

func (s *Store) saveAPIKey(ctx context.Context, userID string, tokenHash []byte, prefix string, now time.Time, replace bool) (APIKey, error) {
	if userID == "" || len(tokenHash) != 32 || prefix == "" {
		return APIKey{}, ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APIKey{}, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx,
		"SELECT status FROM users WHERE id = ? AND deleted_at IS NULL FOR UPDATE", userID,
	).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, ErrNotFound
	} else if err != nil {
		return APIKey{}, err
	}
	if status != UserActive {
		return APIKey{}, ErrConflict
	}
	if replace {
		if _, err := tx.ExecContext(ctx, "DELETE FROM api_keys WHERE user_id = ?", userID); err != nil {
			return APIKey{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return APIKey{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO api_keys
		(id, user_id, token_hash, prefix, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, userID, tokenHash, prefix, now)
	if err != nil {
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return APIKey{}, ErrConflict
		}
		return APIKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return APIKey{}, err
	}
	return APIKey{ID: id, UserID: userID, Prefix: prefix, CreatedAt: now}, nil
}

func (s *Store) GetAPIKey(ctx context.Context, userID string) (APIKey, error) {
	var key APIKey
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, prefix, created_at, last_used_at, revoked_at
		FROM api_keys WHERE user_id = ? LIMIT 1`, userID).Scan(
		&key.ID, &key.UserID, &key.Prefix, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, ErrNotFound
	}
	return key, err
}

func (s *Store) FindAPIKey(ctx context.Context, tokenHash []byte, now time.Time) (APIKey, User, error) {
	var key APIKey
	var user User
	err := s.db.QueryRowContext(ctx, `SELECT k.id, k.user_id, k.prefix, k.created_at, k.last_used_at, k.revoked_at,
		u.id, u.username, u.display_name, u.password_hash, u.group_code, u.status,
		u.created_at, u.updated_at, u.last_login_at, u.deleted_at
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.token_hash = ? AND k.revoked_at IS NULL
			AND u.status = ? AND u.deleted_at IS NULL
		LIMIT 1`, tokenHash, UserActive).Scan(
		&key.ID, &key.UserID, &key.Prefix, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt,
		&user.ID, &user.Username, &user.DisplayName, &user.PasswordHash, &user.GroupCode, &user.Status,
		&user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt, &user.DeletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, User{}, ErrNotFound
	}
	return key, user, err
}

func (s *Store) TouchAPIKey(ctx context.Context, keyID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE api_keys SET last_used_at = ? WHERE id = ? AND revoked_at IS NULL", at, keyID)
	return err
}

func (s *Store) RevokeAPIKey(ctx context.Context, userID string) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE api_keys SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
		time.Now().UTC(), userID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if _, err := s.GetAPIKey(ctx, userID); err == nil {
			return nil
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.GetUser(ctx, userID); err != nil {
			return err
		}
	}
	return nil
}
