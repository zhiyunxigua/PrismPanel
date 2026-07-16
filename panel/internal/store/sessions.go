package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) CreateSession(
	ctx context.Context,
	tokenHash []byte,
	userID string,
	now time.Time,
	expiresAt time.Time,
	idleExpiresAt time.Time,
	sourceIP string,
	userAgent string,
) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions
			(token_hash, user_id, created_at, last_seen_at, expires_at, idle_expires_at, source_ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, now, now, expiresAt, idleExpiresAt, sourceIP, userAgent,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) FindSession(ctx context.Context, tokenHash []byte, now time.Time) (Session, error) {
	row := s.db.QueryRowContext(
		ctx,
		`SELECT s.token_hash, s.created_at, s.last_seen_at, s.expires_at, s.idle_expires_at,
			u.id, u.username, u.display_name, u.password_hash, u.group_code, u.status,
			u.created_at, u.updated_at, u.last_login_at, u.deleted_at
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ?
		   AND s.revoked_at IS NULL
		   AND s.expires_at > ?
		   AND s.idle_expires_at > ?
		   AND u.status = ?
		   AND u.deleted_at IS NULL
		 LIMIT 1`,
		tokenHash, now, now, UserActive,
	)
	var session Session
	err := row.Scan(
		&session.TokenHash, &session.CreatedAt, &session.LastSeenAt,
		&session.ExpiresAt, &session.IdleExpiresAt,
		&session.User.ID, &session.User.Username, &session.User.DisplayName,
		&session.User.PasswordHash, &session.User.GroupCode, &session.User.Status,
		&session.User.CreatedAt, &session.User.UpdatedAt,
		&session.User.LastLoginAt, &session.User.DeletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) TouchSession(
	ctx context.Context,
	tokenHash []byte,
	lastSeenAt time.Time,
	idleExpiresAt time.Time,
) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE sessions
		 SET last_seen_at = ?, idle_expires_at = ?
		 WHERE token_hash = ? AND revoked_at IS NULL`,
		lastSeenAt, idleExpiresAt, tokenHash,
	)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL",
		time.Now().UTC(), tokenHash,
	)
	return err
}
