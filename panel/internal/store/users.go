package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const userColumns = `id, username, display_name, password_hash, group_code, status,
	created_at, updated_at, last_login_at, deleted_at`

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CreateInitialAdmin(ctx context.Context, user NewUser) (User, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if count != 0 {
		return User{}, ErrConflict
	}
	created, err := insertUser(ctx, tx, user)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return created, nil
}

func (s *Store) CreateUser(ctx context.Context, user NewUser) (User, error) {
	return insertUser(ctx, s.db, user)
}

type queryExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertUser(ctx context.Context, executor queryExecer, user NewUser) (User, error) {
	now := time.Now().UTC()
	_, err := executor.ExecContext(
		ctx,
		`INSERT INTO users
			(id, username, display_name, password_hash, group_code, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.DisplayName, user.PasswordHash, user.GroupCode, UserActive, now, now,
	)
	if err != nil {
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return User{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
		PasswordHash: user.PasswordHash, GroupCode: user.GroupCode, Status: UserActive,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Store) FindUserByUsername(ctx context.Context, username string) (User, error) {
	row := s.db.QueryRowContext(
		ctx,
		"SELECT "+userColumns+" FROM users WHERE username = ? LIMIT 1",
		username,
	)
	return scanUser(row)
}

func (s *Store) GetUser(ctx context.Context, userID string) (User, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ? LIMIT 1", userID)
	return scanUser(row)
}

func (s *Store) ListUsers(ctx context.Context, filter UserFilter) (UserList, error) {
	conditions := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 4)
	if filter.Search != "" {
		conditions = append(conditions, "(username LIKE ? OR display_name LIKE ?)")
		search := "%" + filter.Search + "%"
		args = append(args, search, search)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := s.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM users WHERE "+where,
		args...,
	).Scan(&total); err != nil {
		return UserList{}, fmt.Errorf("count filtered users: %w", err)
	}
	queryArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT "+userColumns+" FROM users WHERE "+where+
			" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		queryArgs...,
	)
	if err != nil {
		return UserList{}, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	items := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return UserList{}, err
		}
		items = append(items, user)
	}
	if err := rows.Err(); err != nil {
		return UserList{}, err
	}
	return UserList{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Store) UpdateUser(ctx context.Context, userID string, changes UserChanges) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	current, err := scanUser(tx.QueryRowContext(
		ctx,
		"SELECT "+userColumns+" FROM users WHERE id = ? FOR UPDATE",
		userID,
	))
	if err != nil {
		return User{}, err
	}
	removesActiveSuperAdmin := current.GroupCode == GroupSuperAdmin && current.Status == UserActive &&
		(changes.GroupCode != GroupSuperAdmin || changes.Status != UserActive)
	if removesActiveSuperAdmin {
		if err := ensureAnotherSuperAdmin(ctx, tx, userID); err != nil {
			return User{}, err
		}
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(
		ctx,
		"UPDATE users SET display_name = ?, group_code = ?, status = ?, updated_at = ? WHERE id = ?",
		changes.DisplayName, changes.GroupCode, changes.Status, now, userID,
	); err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	if current.GroupCode != changes.GroupCode {
		if _, err := tx.ExecContext(ctx, "DELETE FROM user_permission_overrides WHERE user_id = ?", userID); err != nil {
			return User{}, fmt.Errorf("clear user permission overrides: %w", err)
		}
	}
	if changes.Status != UserActive {
		if _, err := tx.ExecContext(
			ctx,
			"UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
			now, userID,
		); err != nil {
			return User{}, fmt.Errorf("revoke user sessions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	current.DisplayName = changes.DisplayName
	current.GroupCode = changes.GroupCode
	current.Status = changes.Status
	current.UpdatedAt = now
	return current, nil
}

func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanUser(tx.QueryRowContext(
		ctx,
		"SELECT "+userColumns+" FROM users WHERE id = ? FOR UPDATE",
		userID,
	))
	if err != nil {
		return err
	}
	if current.GroupCode == GroupSuperAdmin && current.Status == UserActive {
		if err := ensureAnotherSuperAdmin(ctx, tx, userID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(
		ctx,
		"UPDATE users SET group_code = ?, status = ?, deleted_at = ?, updated_at = ? WHERE id = ?",
		GroupObserver, UserDeleted, now, now, userID,
	); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"DELETE FROM user_permission_overrides WHERE user_id = ?",
		userID,
	); err != nil {
		return fmt.Errorf("delete user permission overrides: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
		now, userID,
	); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return tx.Commit()
}

func ensureAnotherSuperAdmin(ctx context.Context, tx *transaction, excludedUserID string) error {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT id FROM users
		 WHERE group_code = ? AND status = ? AND deleted_at IS NULL
		 FOR UPDATE`,
		GroupSuperAdmin, UserActive,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if id != excludedUserID {
			count++
		}
	}
	if count == 0 {
		return ErrLastSuperAdmin
	}
	return rows.Err()
}

func (s *Store) SetPassword(ctx context.Context, userID, passwordHash string, keepSessionHash []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(
		ctx,
		"UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		passwordHash, now, userID,
	)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	query := "UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL"
	args := []any{now, userID}
	if len(keepSessionHash) > 0 {
		query += " AND token_hash <> ?"
		args = append(args, keepSessionHash)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RevokeUserSessions(ctx context.Context, userID string) error {
	result, err := s.db.ExecContext(
		ctx,
		"UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
		time.Now().UTC(), userID,
	)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if _, err := s.GetUser(ctx, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) MarkLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?",
		at, at, userID,
	)
	return err
}

type rowScanner interface {
	Scan(...any) error
}

func scanUser(row rowScanner) (User, error) {
	var user User
	err := row.Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.PasswordHash,
		&user.GroupCode, &user.Status, &user.CreatedAt, &user.UpdatedAt,
		&user.LastLoginAt, &user.DeletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}
