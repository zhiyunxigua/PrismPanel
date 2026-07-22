package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	NetGameRunRunning = "running"
	NetGameRunSuccess = "success"
	NetGameRunFailed  = "failed"

	NetGameDetailsPending = "pending"
	NetGameDetailsOK      = "ok"
	NetGameDetailsFailed  = "failed"
)

type NetGame struct {
	ID                  uint64          `json:"-"`
	GameID              string          `json:"game_id"`
	Name                string          `json:"name"`
	Summary             string          `json:"summary"`
	Author              string          `json:"author"`
	Versions            json.RawMessage `json:"versions"`
	IP                  string          `json:"ip"`
	Port                *uint16         `json:"port,omitempty"`
	Address             string          `json:"address"`
	PublishTime         *int64          `json:"publish_time,omitempty"`
	Images              json.RawMessage `json:"images"`
	Description         string          `json:"description"`
	FirstSeenAt         time.Time       `json:"first_seen_at"`
	LastSeenAt          time.Time       `json:"last_seen_at"`
	DetailsStatus       string          `json:"details_status"`
	DetailsAttemptedAt  *time.Time      `json:"details_attempted_at,omitempty"`
	DetailsUpdatedAt    *time.Time      `json:"details_updated_at,omitempty"`
	DetailsErrorCode    string          `json:"details_error_code,omitempty"`
	DetailsErrorMessage string          `json:"details_error_message,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type NetGameListItem struct {
	GameID      string `json:"game_id"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	OnlineCount uint32 `json:"online_count"`
}

type NetGameDetails struct {
	Author      string
	Versions    []string
	IP          string
	Port        *uint16
	Address     string
	PublishTime *int64
	Images      []string
	Description string
}

type NetGameCollectionRun struct {
	ID              uint64     `json:"id"`
	TriggerType     string     `json:"trigger_type"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	PagesFetched    uint32     `json:"pages_fetched"`
	TotalGames      uint32     `json:"total_games"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	RemoteRequestID string     `json:"remote_request_id,omitempty"`
}

type NetGameObservation struct {
	RunID       uint64 `json:"run_id"`
	GameKey     uint64 `json:"-"`
	GameID      string `json:"game_id,omitempty"`
	OnlineCount uint32 `json:"online_count"`
}

type RankedNetGame struct {
	NetGame
	OnlineCount uint32 `json:"online_count"`
	Rank        int    `json:"rank"`
}

type NetGameRunPoint struct {
	RunID     uint64    `json:"run_id"`
	SampledAt time.Time `json:"sampled_at"`
	Status    string    `json:"status"`
}

type NetGameObservationPoint struct {
	RunID       uint64    `json:"run_id"`
	GameID      string    `json:"game_id"`
	SampledAt   time.Time `json:"sampled_at"`
	OnlineCount uint32    `json:"online_count"`
}

func (s *Store) CreateNetGameCollectionRun(ctx context.Context, triggerType string) (NetGameCollectionRun, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO net_game_collection_runs
		(trigger_type, status, started_at) VALUES (?, ?, ?)`, triggerType, NetGameRunRunning, now)
	if err != nil {
		return NetGameCollectionRun{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return NetGameCollectionRun{}, err
	}
	return NetGameCollectionRun{ID: uint64(id), TriggerType: triggerType, Status: NetGameRunRunning, StartedAt: now}, nil
}

func (s *Store) CompleteNetGameCollectionRun(
	ctx context.Context,
	runID uint64,
	pagesFetched, totalGames uint32,
	games []NetGameListItem,
	remoteRequestID string,
) (NetGameCollectionRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NetGameCollectionRun{}, err
	}
	defer tx.Rollback()

	run, err := readNetGameCollectionRun(tx.QueryRowContext(ctx, `SELECT id, trigger_type, status,
		started_at, finished_at, pages_fetched, total_games, error_code, error_message, remote_request_id
		FROM net_game_collection_runs WHERE id = ? FOR UPDATE`, runID))
	if err != nil {
		return NetGameCollectionRun{}, err
	}
	if run.Status != NetGameRunRunning {
		return run, nil
	}

	seen := make(map[string]struct{}, len(games))
	for _, item := range games {
		item.GameID = strings.TrimSpace(item.GameID)
		if item.GameID == "" {
			return NetGameCollectionRun{}, fmt.Errorf("game id is required")
		}
		if _, exists := seen[item.GameID]; exists {
			return NetGameCollectionRun{}, fmt.Errorf("duplicate game id %q", item.GameID)
		}
		seen[item.GameID] = struct{}{}
		if err := s.upsertNetGameObservation(ctx, tx, runID, item); err != nil {
			return NetGameCollectionRun{}, err
		}
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE net_game_collection_runs
		SET status = ?, finished_at = ?, pages_fetched = ?, total_games = ?, remote_request_id = ?
		WHERE id = ? AND status = ?`,
		NetGameRunSuccess, now, pagesFetched, totalGames, remoteRequestID, runID, NetGameRunRunning); err != nil {
		return NetGameCollectionRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return NetGameCollectionRun{}, err
	}
	return s.GetNetGameCollectionRun(ctx, runID)
}

func (s *Store) FailNetGameCollectionRun(ctx context.Context, runID uint64, code, message, remoteRequestID string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE net_game_collection_runs
		SET status = ?, finished_at = ?, error_code = ?, error_message = ?, remote_request_id = ?
		WHERE id = ? AND status = ?`,
		NetGameRunFailed, now, code, trimNetGameError(message), remoteRequestID, runID, NetGameRunRunning)
	return err
}

func (s *Store) MarkInterruptedNetGameRuns(ctx context.Context) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE net_game_collection_runs
		SET status = ?, finished_at = ?, error_code = ?, error_message = ? WHERE status = ?`,
		NetGameRunFailed, now, "PANEL_INTERRUPTED", "Panel stopped before the collection run finished", NetGameRunRunning)
	return err
}

func (s *Store) GetNetGameCollectionRun(ctx context.Context, runID uint64) (NetGameCollectionRun, error) {
	return readNetGameCollectionRun(s.db.QueryRowContext(ctx, `SELECT id, trigger_type, status,
		started_at, finished_at, pages_fetched, total_games, error_code, error_message, remote_request_id
		FROM net_game_collection_runs WHERE id = ?`, runID))
}

func (s *Store) LatestNetGameRun(ctx context.Context, status string) (NetGameCollectionRun, error) {
	query := `SELECT id, trigger_type, status, started_at, finished_at, pages_fetched,
		total_games, error_code, error_message, remote_request_id FROM net_game_collection_runs`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY started_at DESC, id DESC LIMIT 1`
	return readNetGameCollectionRun(s.db.QueryRowContext(ctx, query, args...))
}

func (s *Store) LatestSuccessfulNetGameRun(ctx context.Context) (NetGameCollectionRun, error) {
	return s.LatestNetGameRun(ctx, NetGameRunSuccess)
}

func (s *Store) GetNetGame(ctx context.Context, gameID string) (NetGame, error) {
	return readNetGame(s.db.QueryRowContext(ctx, netGameByIDQuery+` WHERE g.game_id = ?`, strings.TrimSpace(gameID)))
}

func (s *Store) NetGamesNeedingDetails(ctx context.Context, limit int) ([]NetGame, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, netGameByIDQuery+` WHERE g.details_status <> ? OR g.details_updated_at IS NULL
		ORDER BY CASE g.details_status WHEN ? THEN 0 WHEN ? THEN 2 ELSE 1 END,
		CASE WHEN g.details_updated_at IS NULL THEN 0 ELSE 1 END,
		g.details_updated_at ASC, g.details_attempted_at ASC, g.last_seen_at DESC
		LIMIT ?`, NetGameDetailsOK, NetGameDetailsPending, NetGameDetailsFailed, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NetGame, 0)
	for rows.Next() {
		item, err := readNetGame(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateNetGameDetails(ctx context.Context, gameID string, details NetGameDetails) (NetGame, error) {
	now := time.Now().UTC()
	versions, err := json.Marshal(details.Versions)
	if err != nil {
		return NetGame{}, fmt.Errorf("encode versions: %w", err)
	}
	images, err := json.Marshal(details.Images)
	if err != nil {
		return NetGame{}, fmt.Errorf("encode images: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO net_games
		(game_id, name, summary, author, versions, ip, port, address, publish_time, images,
		 description, first_seen_at, last_seen_at, details_status, details_attempted_at,
		 details_updated_at, details_error_code, details_error_message, created_at, updated_at)
		VALUES (?, '', '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?)
		ON DUPLICATE KEY UPDATE author = VALUES(author), versions = VALUES(versions), ip = VALUES(ip),
		port = VALUES(port), address = VALUES(address), publish_time = VALUES(publish_time),
		images = VALUES(images), description = VALUES(description), details_status = ?,
		details_attempted_at = ?, details_updated_at = ?, details_error_code = '',
		details_error_message = '', updated_at = ?`,
		strings.TrimSpace(gameID), details.Author, versions, details.IP, details.Port,
		details.Address, details.PublishTime, images, details.Description, now, now,
		NetGameDetailsOK, now, now, now, now,
		NetGameDetailsOK, now, now, now)
	if err != nil {
		return NetGame{}, err
	}
	if _, err := result.LastInsertId(); err != nil {
		return NetGame{}, err
	}
	return s.GetNetGame(ctx, gameID)
}

func (s *Store) MarkNetGameDetailsFailed(ctx context.Context, gameID, code, message string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE net_games SET details_status = ?, details_attempted_at = ?,
		details_error_code = ?, details_error_message = ?, updated_at = ? WHERE game_id = ?`,
		NetGameDetailsFailed, now, code, trimNetGameError(message), now, strings.TrimSpace(gameID))
	return err
}

func (s *Store) ReplaceNetGameSnapshot(ctx context.Context, runID uint64, pagesFetched uint32, games []NetGameListItem) (NetGameCollectionRun, error) {
	run, err := s.CompleteNetGameCollectionRun(ctx, runID, pagesFetched, uint32(len(games)), games, "")
	if err != nil {
		return NetGameCollectionRun{}, err
	}
	return run, nil
}

func (s *Store) NetGameRunsBetween(ctx context.Context, from, to time.Time) ([]NetGameCollectionRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, trigger_type, status, started_at, finished_at,
		pages_fetched, total_games, error_code, error_message, remote_request_id
		FROM net_game_collection_runs WHERE started_at >= ? AND started_at < ?
		ORDER BY started_at ASC, id ASC`, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NetGameCollectionRun, 0)
	for rows.Next() {
		item, err := readNetGameCollectionRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) NetGameObservationsBetween(ctx context.Context, from, to time.Time) ([]NetGameObservationPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, g.game_id, r.started_at, o.online_count
		FROM net_game_observations o
		JOIN net_game_collection_runs r ON r.id = o.run_id
		JOIN net_games g ON g.id = o.game_key
		WHERE r.status = ? AND r.started_at >= ? AND r.started_at < ?
		ORDER BY r.started_at ASC, g.game_id ASC`, NetGameRunSuccess, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NetGameObservationPoint, 0)
	for rows.Next() {
		var point NetGameObservationPoint
		if err := rows.Scan(&point.RunID, &point.GameID, &point.SampledAt, &point.OnlineCount); err != nil {
			return nil, err
		}
		result = append(result, point)
	}
	return result, rows.Err()
}

func (s *Store) EarliestNetGameSuccess(ctx context.Context) (time.Time, error) {
	var value time.Time
	err := s.db.QueryRowContext(ctx, `SELECT started_at FROM net_game_collection_runs
		WHERE status = ? ORDER BY started_at ASC, id ASC LIMIT 1`, NetGameRunSuccess).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	return value, err
}

func (s *Store) RankedGamesForRun(ctx context.Context, runID uint64, forcedIDs []string, limit int) ([]RankedNetGame, error) {
	if limit < 1 || limit > 100 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, netGameWithObservationQuery+` WHERE o.run_id = ?
		ORDER BY o.online_count DESC, g.name ASC, g.game_id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]RankedNetGame, 0)
	for rows.Next() {
		item, err := readRankedNetGame(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	forcedSet := make(map[string]struct{}, len(forcedIDs))
	filtered := make([]RankedNetGame, 0, limit)
	for _, raw := range forcedIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := forcedSet[id]; exists {
			continue
		}
		forcedSet[id] = struct{}{}
		for _, item := range items {
			if item.GameID == id {
				filtered = append(filtered, item)
				break
			}
		}
	}
	for _, item := range items {
		if len(filtered) >= limit {
			break
		}
		if _, forced := forcedSet[item.GameID]; forced {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	for index := range filtered {
		filtered[index].Rank = index + 1
	}
	return filtered, nil
}

func (s *Store) upsertNetGameObservation(ctx context.Context, tx *transaction, runID uint64, item NetGameListItem) error {
	game, err := s.upsertNetGame(ctx, tx, item)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO net_game_observations (run_id, game_key, online_count)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE online_count = VALUES(online_count)`, runID, game.ID, item.OnlineCount)
	return err
}

func (s *Store) upsertNetGame(ctx context.Context, tx *transaction, item NetGameListItem) (NetGame, error) {
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO net_games
		(game_id, name, summary, author, versions, ip, port, address, publish_time, images,
		 description, first_seen_at, last_seen_at, details_status, details_attempted_at,
		 details_updated_at, details_error_code, details_error_message, created_at, updated_at)
		VALUES (?, ?, ?, '', JSON_ARRAY(), '', NULL, '', NULL, JSON_ARRAY(), '', ?, ?, ?, NULL, NULL, '', '', ?, ?)
		ON DUPLICATE KEY UPDATE name = VALUES(name), summary = VALUES(summary), last_seen_at = VALUES(last_seen_at),
		updated_at = VALUES(updated_at), id = LAST_INSERT_ID(id)`,
		item.GameID, item.Name, item.Summary, now, now, NetGameDetailsPending, now, now)
	if err != nil {
		return NetGame{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return NetGame{}, err
	}
	return NetGame{ID: uint64(id), GameID: item.GameID, Name: item.Name, Summary: item.Summary, FirstSeenAt: now, LastSeenAt: now, DetailsStatus: NetGameDetailsPending, CreatedAt: now, UpdatedAt: now}, nil
}

func readNetGameCollectionRun(row interface{ Scan(...any) error }) (NetGameCollectionRun, error) {
	var item NetGameCollectionRun
	var finishedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.TriggerType, &item.Status, &item.StartedAt, &finishedAt,
		&item.PagesFetched, &item.TotalGames, &item.ErrorCode, &item.ErrorMessage, &item.RemoteRequestID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NetGameCollectionRun{}, ErrNotFound
		}
		return NetGameCollectionRun{}, err
	}
	if finishedAt.Valid {
		item.FinishedAt = &finishedAt.Time
	}
	return item, nil
}

func readNetGame(row interface{ Scan(...any) error }) (NetGame, error) {
	var item NetGame
	var port sql.NullInt64
	var publishTime sql.NullInt64
	var versions []byte
	var images []byte
	var attemptedAt sql.NullTime
	var updatedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.GameID, &item.Name, &item.Summary, &item.Author, &versions, &item.IP,
		&port, &item.Address, &publishTime, &images, &item.Description, &item.FirstSeenAt, &item.LastSeenAt,
		&item.DetailsStatus, &attemptedAt, &updatedAt, &item.DetailsErrorCode, &item.DetailsErrorMessage,
		&item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NetGame{}, ErrNotFound
		}
		return NetGame{}, err
	}
	populateNetGameNulls(&item, port, publishTime, versions, images, attemptedAt, updatedAt)
	return item, nil
}

func readRankedNetGame(row interface{ Scan(...any) error }) (RankedNetGame, error) {
	var item RankedNetGame
	var port sql.NullInt64
	var publishTime sql.NullInt64
	var versions []byte
	var images []byte
	var attemptedAt sql.NullTime
	var updatedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.GameID, &item.Name, &item.Summary, &item.Author, &versions, &item.IP,
		&port, &item.Address, &publishTime, &images, &item.Description, &item.FirstSeenAt, &item.LastSeenAt,
		&item.DetailsStatus, &attemptedAt, &updatedAt, &item.DetailsErrorCode, &item.DetailsErrorMessage,
		&item.CreatedAt, &item.UpdatedAt, &item.OnlineCount); err != nil {
		return RankedNetGame{}, err
	}
	populateNetGameNulls(&item.NetGame, port, publishTime, versions, images, attemptedAt, updatedAt)
	return item, nil
}

func populateNetGameNulls(item *NetGame, port, publishTime sql.NullInt64, versions, images []byte, attemptedAt, updatedAt sql.NullTime) {
	if port.Valid {
		value := uint16(port.Int64)
		item.Port = &value
	}
	if publishTime.Valid {
		value := publishTime.Int64
		item.PublishTime = &value
	}
	if len(versions) > 0 {
		item.Versions = append(json.RawMessage(nil), versions...)
	}
	if len(images) > 0 {
		item.Images = append(json.RawMessage(nil), images...)
	}
	if attemptedAt.Valid {
		item.DetailsAttemptedAt = &attemptedAt.Time
	}
	if updatedAt.Valid {
		item.DetailsUpdatedAt = &updatedAt.Time
	}
}

const netGameByIDQuery = `SELECT g.id, g.game_id, g.name, g.summary, g.author, g.versions, g.ip, g.port,
	g.address, g.publish_time, g.images, g.description, g.first_seen_at, g.last_seen_at,
	g.details_status, g.details_attempted_at, g.details_updated_at, g.details_error_code,
	g.details_error_message, g.created_at, g.updated_at FROM net_games g`

const netGameWithObservationQuery = `SELECT g.id, g.game_id, g.name, g.summary, g.author, g.versions, g.ip, g.port,
	g.address, g.publish_time, g.images, g.description, g.first_seen_at, g.last_seen_at,
	g.details_status, g.details_attempted_at, g.details_updated_at, g.details_error_code,
	g.details_error_message, g.created_at, g.updated_at, o.online_count
	FROM net_game_observations o JOIN net_games g ON g.id = o.game_key`

func trimNetGameError(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 500 {
		return value
	}
	runes := []rune(value)
	return string(runes[:500])
}
