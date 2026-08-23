package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MCServerStatus 是国际服的最后一次 ping 状态。
const (
	MCServerStatusUnknown = "unknown"
	MCServerStatusOK      = "ok"
	MCServerStatusFailed  = "failed"

	mcMaxStoredText = 500
)

// MCServer 是面板手动维护的国际版（Minecraft Java 版）服务器。
type MCServer struct {
	ID            uint64     `json:"id"`
	Name          string     `json:"name"`
	ServerKey     string     `json:"server_key"` // 规范化 host:port 唯一键
	Host          string     `json:"host"`
	Port          uint16     `json:"port"`
	Enabled       bool       `json:"enabled"`
	Note          string     `json:"note"`
	LastStatus    string     `json:"last_status"`
	LastOnline    *uint32    `json:"last_online,omitempty"`
	LastMax       *uint32    `json:"last_max,omitempty"`
	LastLatencyMS *uint32    `json:"last_latency_ms,omitempty"`
	LastVersion   string     `json:"last_version"`
	LastError     string     `json:"last_error"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// MCServerInput 是创建/更新国际服的参数。ServerKey 由上层（netgames 服务）计算，
// 为空时 store 会自行根据 host/port 规范化生成。
type MCServerInput struct {
	Name      string
	ServerKey string
	Host      string
	Port      uint16
	Enabled   *bool // nil 表示保持默认/原值
	Note      string
}

// MCServerObservation 是国际服单次成功的采样点。
type MCServerObservation struct {
	ID          uint64    `json:"id"`
	ServerID    uint64    `json:"server_id"`
	SampledAt   time.Time `json:"sampled_at"`
	Online      uint32    `json:"online"`
	MaxPlayers  uint32    `json:"max_players"`
	LatencyMS   uint32    `json:"latency_ms"`
	VersionName string    `json:"version_name"`
	Protocol    int       `json:"protocol"`
}

// MCServerObservationPoint 是带服务器信息的查询结果。
type MCServerObservationPoint struct {
	ServerKey   string    `json:"server_key"`
	ServerName  string    `json:"server_name"`
	SampledAt   time.Time `json:"sampled_at"`
	Online      uint32    `json:"online"`
	MaxPlayers  uint32    `json:"max_players"`
	LatencyMS   uint32    `json:"latency_ms"`
	VersionName string    `json:"version_name"`
}

// NormalizeMCServerHost 规范化主机名（去空白、转小写、去末尾点）。
func NormalizeMCServerHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

// MCServerKeyOf 生成规范化唯一键 "host:port"（IPv6 加方括号）。
func MCServerKeyOf(host string, port uint16) string {
	host = NormalizeMCServerHost(host)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func (s *Store) CreateMCServer(ctx context.Context, input MCServerInput) (MCServer, error) {
	now := time.Now().UTC()
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	host := NormalizeMCServerHost(input.Host)
	key := strings.TrimSpace(input.ServerKey)
	if key == "" {
		key = MCServerKeyOf(host, input.Port)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO mc_servers
		(name, game_id, host, port, enabled, note, last_status, last_version, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', '', ?, ?)`,
		strings.TrimSpace(input.Name), key, host, input.Port, enabled, strings.TrimSpace(input.Note),
		MCServerStatusUnknown, now, now)
	if err != nil {
		if isDuplicateKeyError(err) {
			return MCServer{}, fmt.Errorf("%w: 服务器 %s 已存在", ErrConflict, key)
		}
		return MCServer{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return MCServer{}, err
	}
	return s.GetMCServer(ctx, uint64(id))
}

func (s *Store) UpdateMCServer(ctx context.Context, id uint64, input MCServerInput) (MCServer, error) {
	current, err := s.GetMCServer(ctx, id)
	if err != nil {
		return MCServer{}, err
	}
	host := NormalizeMCServerHost(input.Host)
	if host == "" {
		return MCServer{}, errors.New("服务器地址不能为空")
	}
	key := strings.TrimSpace(input.ServerKey)
	if key == "" {
		key = MCServerKeyOf(host, input.Port)
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `UPDATE mc_servers SET name = ?, game_id = ?, host = ?, port = ?,
		enabled = ?, note = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(input.Name), key, host, input.Port,
		boolOr(input.Enabled, current.Enabled), strings.TrimSpace(input.Note), now, id)
	if err != nil {
		if isDuplicateKeyError(err) {
			return MCServer{}, fmt.Errorf("%w: 服务器 %s 已存在", ErrConflict, key)
		}
		return MCServer{}, err
	}
	return s.GetMCServer(ctx, id)
}

func (s *Store) DeleteMCServer(ctx context.Context, id uint64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM mc_servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetMCServer(ctx context.Context, id uint64) (MCServer, error) {
	return readMCServer(s.db.QueryRowContext(ctx, mcServerColumns+` WHERE m.id = ?`, id))
}

func (s *Store) GetMCServerByKey(ctx context.Context, serverKey string) (MCServer, error) {
	return readMCServer(s.db.QueryRowContext(ctx, mcServerColumns+` WHERE m.game_id = ?`, serverKey))
}

func (s *Store) ListMCServers(ctx context.Context) ([]MCServer, error) {
	return s.listMCServers(ctx, "")
}

func (s *Store) ListEnabledMCServers(ctx context.Context) ([]MCServer, error) {
	return s.listMCServers(ctx, ` WHERE m.enabled = 1`)
}

func (s *Store) listMCServers(ctx context.Context, condition string) ([]MCServer, error) {
	rows, err := s.db.QueryContext(ctx, mcServerColumns+condition+` ORDER BY m.enabled DESC, m.name ASC, m.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]MCServer, 0)
	for rows.Next() {
		item, err := readMCServer(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// UpdateMCServerResult 写入最近一次 ping 的结果（状态/在线/最大/延迟/版本/错误）。
func (s *Store) UpdateMCServerResult(
	ctx context.Context,
	id uint64,
	status string,
	online, maxPlayers *uint32,
	latencyMS *uint32,
	version string,
	errorMessage string,
) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE mc_servers SET last_status = ?, last_online = ?,
		last_max = ?, last_latency_ms = ?, last_version = ?, last_error = ?, last_checked_at = ?,
		updated_at = ? WHERE id = ?`,
		status, online, maxPlayers, latencyMS, truncateMCStoredText(version),
		truncateMCStoredText(errorMessage), now, now, id)
	return err
}

func (s *Store) CreateMCServerObservation(ctx context.Context, observation MCServerObservation) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO mc_server_observations
		(server_id, sampled_at, online, max_players, latency_ms, version_name, protocol)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		observation.ServerID, observation.SampledAt.UTC(), observation.Online, observation.MaxPlayers,
		observation.LatencyMS, truncateMCStoredText(observation.VersionName), observation.Protocol)
	return err
}

// MCServerObservationsBetweenForServers 查询若干服务器在一段时间内的采样点。
func (s *Store) MCServerObservationsBetweenForServers(
	ctx context.Context,
	serverIDs []uint64,
	from, to time.Time,
) ([]MCServerObservationPoint, error) {
	ids := uniqueMCServerIDs(serverIDs)
	if len(ids) == 0 {
		return []MCServerObservationPoint{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, id)
	}
	args = append(args, from.UTC(), to.UTC())
	rows, err := s.db.QueryContext(ctx, `SELECT m.game_id, m.name, o.sampled_at, o.online,
		o.max_players, o.latency_ms, o.version_name
		FROM mc_server_observations o JOIN mc_servers m ON m.id = o.server_id
		WHERE o.server_id IN (`+strings.Join(placeholders, ",")+`)
		AND o.sampled_at >= ? AND o.sampled_at < ?
		ORDER BY o.sampled_at ASC, o.id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]MCServerObservationPoint, 0)
	for rows.Next() {
		var point MCServerObservationPoint
		if err := rows.Scan(&point.ServerKey, &point.ServerName, &point.SampledAt, &point.Online,
			&point.MaxPlayers, &point.LatencyMS, &point.VersionName); err != nil {
			return nil, err
		}
		result = append(result, point)
	}
	return result, rows.Err()
}

func (s *Store) LatestMCServerObservationTime(ctx context.Context, serverID uint64) (time.Time, error) {
	var value time.Time
	err := s.db.QueryRowContext(ctx, `SELECT sampled_at FROM mc_server_observations
		WHERE server_id = ? ORDER BY sampled_at DESC, id DESC LIMIT 1`, serverID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	return value, err
}

func (s *Store) DeleteMCServerObservationsBefore(ctx context.Context, serverID uint64, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM mc_server_observations
		WHERE server_id = ? AND sampled_at < ?`, serverID, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

const mcServerColumns = `SELECT m.id, m.name, m.game_id, m.host, m.port, m.enabled, m.note,
	m.last_status, m.last_online, m.last_max, m.last_latency_ms, m.last_version, m.last_error,
	m.last_checked_at, m.created_at, m.updated_at FROM mc_servers m`

func readMCServer(row interface{ Scan(...any) error }) (MCServer, error) {
	var item MCServer
	var online sql.NullInt64
	var maximum sql.NullInt64
	var latency sql.NullInt64
	var checkedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.Name, &item.ServerKey, &item.Host, &item.Port, &item.Enabled,
		&item.Note, &item.LastStatus, &online, &maximum, &latency, &item.LastVersion, &item.LastError,
		&checkedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MCServer{}, ErrNotFound
		}
		return MCServer{}, err
	}
	if online.Valid {
		value := uint32(online.Int64)
		item.LastOnline = &value
	}
	if maximum.Valid {
		value := uint32(maximum.Int64)
		item.LastMax = &value
	}
	if latency.Valid {
		value := uint32(latency.Int64)
		item.LastLatencyMS = &value
	}
	if checkedAt.Valid {
		item.LastCheckedAt = &checkedAt.Time
	}
	return item, nil
}

func uniqueMCServerIDs(values []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(values))
	result := make([]uint64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func truncateMCStoredText(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= mcMaxStoredText {
		return value
	}
	return string(runes[:mcMaxStoredText])
}

func boolOr(value *bool, fallback bool) bool {
	if value != nil {
		return *value
	}
	return fallback
}

// isDuplicateKeyError 识别 MySQL 1062 与 SQLite 唯一约束冲突。
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate entry") ||
		strings.Contains(text, "unique constraint") ||
		strings.Contains(text, "constraint failed") ||
		strings.Contains(text, "already exists")
}
