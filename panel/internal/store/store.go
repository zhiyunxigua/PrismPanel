package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"PrismPanel/internal/config"
	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *database
}

func Open(ctx context.Context, cfg config.DatabaseConfig) (*Store, error) {
	if strings.EqualFold(cfg.Type, "sqlite") {
		return openSQLite(ctx, cfg)
	}
	raw, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	raw.SetMaxOpenConns(cfg.MaxOpenConns)
	raw.SetMaxIdleConns(cfg.MaxIdleConns)
	raw.SetConnMaxLifetime(30 * time.Minute)
	if err := raw.PingContext(ctx); err != nil {
		raw.Close()
		// 本机无 MySQL 时跳过，回退到本地 SQLite，便于本地测试
		slog.Warn("connect mysql failed, falling back to sqlite", "error", err)
		return openSQLite(ctx, cfg)
	}
	store := &Store{db: &database{DB: raw, prefix: cfg.TablePrefix}}
	if err := store.initializeSchema(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return store, nil
}

func openSQLite(ctx context.Context, cfg config.DatabaseConfig) (*Store, error) {
	path := strings.TrimSpace(cfg.SQLitePath)
	if path == "" {
		path = "panel.db"
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite 单写者限制，本地测试场景一个连接足够
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	if err := raw.PingContext(ctx); err != nil {
		raw.Close()
		return nil, fmt.Errorf("connect sqlite %s: %w", path, err)
	}
	store := &Store{db: &database{DB: raw, prefix: cfg.TablePrefix, sqlite: true}}
	if err := store.initializeSchema(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	slog.Info("panel store opened with sqlite", "path", path)
	return store, nil
}

var logicalTablePattern = regexp.MustCompile(
	`\b(user_permission_overrides|group_permissions|user_preferences|user_groups|sessions|users|nodes|audit_logs|file_operations|operator_state|operators|scheduled_task_targets|scheduled_tasks|task_run_targets|task_runs|plugin_artifacts_v2|plugin_artifacts|proxy_sync_owners|proxy_sync_rules|plugin_deploy_preferences|mc_servers|mc_server_observations)\b`,
)

type database struct {
	*sql.DB
	prefix string
	sqlite bool
}

type transaction struct {
	*sql.Tx
	prefix string
	sqlite bool
}

func (d *database) statement(query string) string {
	query = prefixStatement(query, d.prefix)
	if d.sqlite {
		query = toSQLite(query)
	}
	return query
}

func (d *database) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.DB.ExecContext(ctx, d.statement(query), args...)
}

func (d *database) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, d.statement(query), args...)
}

func (d *database) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.DB.QueryRowContext(ctx, d.statement(query), args...)
}

func (d *database) BeginTx(ctx context.Context, options *sql.TxOptions) (*transaction, error) {
	tx, err := d.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &transaction{Tx: tx, prefix: d.prefix, sqlite: d.sqlite}, nil
}

func (t *transaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(ctx, t.statement(query), args...)
}

func (t *transaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(ctx, t.statement(query), args...)
}

func (t *transaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(ctx, t.statement(query), args...)
}

func (t *transaction) statement(query string) string {
	query = prefixStatement(query, t.prefix)
	if t.sqlite {
		query = toSQLite(query)
	}
	return query
}

func prefixStatement(query, prefix string) string {
	return logicalTablePattern.ReplaceAllStringFunc(query, func(table string) string {
		return "`" + prefix + table + "`"
	})
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initializeSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS user_groups (
			code VARCHAR(32) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			description VARCHAR(500) NOT NULL DEFAULT '',
			sort_order INT NOT NULL DEFAULT 100,
			built_in BOOLEAN NOT NULL DEFAULT FALSE,
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			UNIQUE KEY uq_user_groups_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`INSERT IGNORE INTO user_groups
			(code, name, description, sort_order, built_in, created_at, updated_at) VALUES
			('super_admin', '超级管理员', '拥有全部系统权限，权限不可裁剪', 10, TRUE, CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
			('admin', '管理员', '负责面板配置、节点和用户日常管理', 20, TRUE, CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
			('operator', '运维人员', '负责实例、控制台、文件、部署和玩家操作', 30, TRUE, CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
			('observer', '观察者', '只读查看运行状态和业务信息', 40, TRUE, CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6))`,
		`CREATE TABLE IF NOT EXISTS users (
			id CHAR(32) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			username VARCHAR(64) NOT NULL,
			display_name VARCHAR(100) NOT NULL,
			password_hash VARCHAR(255) CHARACTER SET ascii NOT NULL,
			group_code VARCHAR(32) CHARACTER SET ascii NOT NULL,
			status VARCHAR(16) CHARACTER SET ascii NOT NULL,
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			last_login_at DATETIME(6) NULL,
			deleted_at DATETIME(6) NULL,
			UNIQUE KEY uq_users_username (username),
			KEY idx_users_status_group (status, group_code),
			CONSTRAINT fk_users_group FOREIGN KEY (group_code) REFERENCES user_groups(code)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash BINARY(32) NOT NULL PRIMARY KEY,
			user_id CHAR(32) CHARACTER SET ascii NOT NULL,
			created_at DATETIME(6) NOT NULL,
			last_seen_at DATETIME(6) NOT NULL,
			expires_at DATETIME(6) NOT NULL,
			idle_expires_at DATETIME(6) NOT NULL,
			revoked_at DATETIME(6) NULL,
			source_ip VARCHAR(45) CHARACTER SET ascii NOT NULL,
			user_agent VARCHAR(255) NOT NULL,
			CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			KEY idx_sessions_user_active (user_id, revoked_at),
			KEY idx_sessions_expiry (expires_at, idle_expires_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS group_permissions (
			group_code VARCHAR(32) CHARACTER SET ascii NOT NULL,
			permission_code VARCHAR(64) CHARACTER SET ascii NOT NULL,
			PRIMARY KEY (group_code, permission_code),
			CONSTRAINT fk_group_permissions_group FOREIGN KEY (group_code) REFERENCES user_groups(code) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`INSERT IGNORE INTO group_permissions (group_code, permission_code) VALUES
			('admin','dashboard.view'),
			('admin','node.view'),('admin','node.create'),('admin','node.update'),('admin','node.delete'),('admin','node.terminal'),
			('admin','server.view'),('admin','server.create'),('admin','server.configure'),('admin','server.deploy'),('admin','server.delete'),
			('admin','instance.start'),('admin','instance.stop'),('admin','instance.restart'),('admin','instance.kill'),
			('admin','console.read'),('admin','console.command'),('admin','file.read'),('admin','file.write'),('admin','file.delete'),
			('admin','player.view'),('admin','player.kick'),('admin','player.message'),('admin','player.transfer'),('admin','player.whitelist.manage'),
			('admin','plugin.view'),('admin','plugin.upload'),('admin','plugin.deploy'),('admin','plugin.remove'),
			('admin','firewall.view'),('admin','firewall.manage'),('admin','task.view'),('admin','task.cancel'),('admin','task.retry'),
			('admin','schedule.view'),('admin','schedule.manage'),
			('admin','alert.view'),('admin','alert.acknowledge'),('admin','audit.view'),
			('admin','user.view'),('admin','user.create'),('admin','user.update'),('admin','user.delete'),('admin','user.password.reset'),('admin','user.sessions.revoke'),
			('operator','dashboard.view'),('operator','node.view'),('operator','server.view'),('operator','server.deploy'),
			('operator','instance.start'),('operator','instance.stop'),('operator','instance.restart'),('operator','instance.kill'),
			('operator','console.read'),('operator','console.command'),('operator','file.read'),('operator','file.write'),('operator','file.delete'),
			('operator','player.view'),('operator','player.kick'),('operator','player.message'),('operator','player.transfer'),('operator','player.whitelist.manage'),
			('operator','plugin.view'),('operator','plugin.deploy'),('operator','task.view'),('operator','alert.view'),
			('operator','schedule.view'),('operator','schedule.manage'),
			('observer','dashboard.view'),('observer','node.view'),('observer','server.view'),('observer','console.read'),('observer','file.read'),
			('observer','player.view'),('observer','plugin.view'),('observer','firewall.view'),('observer','task.view'),('observer','schedule.view'),('observer','alert.view')`,
		`CREATE TABLE IF NOT EXISTS user_permission_overrides (
			user_id CHAR(32) CHARACTER SET ascii NOT NULL,
			permission_code VARCHAR(64) CHARACTER SET ascii NOT NULL,
			allowed BOOLEAN NOT NULL,
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (user_id, permission_code),
			CONSTRAINT fk_user_permission_overrides_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id CHAR(32) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			daemon_id VARCHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			name VARCHAR(100) NOT NULL,
			base_url VARCHAR(512) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			public_url VARCHAR(512) CHARACTER SET ascii NOT NULL DEFAULT '',
			token_ciphertext BLOB NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			daemon_version VARCHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			protocol_version VARCHAR(32) CHARACTER SET ascii NOT NULL DEFAULT '',
			capabilities JSON NULL,
			last_connected_at DATETIME(6) NULL,
			last_error VARCHAR(500) NOT NULL DEFAULT '',
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			UNIQUE KEY uq_nodes_base_url (base_url)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id CHAR(32) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			request_id CHAR(32) CHARACTER SET ascii NOT NULL,
			created_at DATETIME(6) NOT NULL,
			actor_user_id CHAR(32) CHARACTER SET ascii NULL,
			session_id CHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			actor_username VARCHAR(64) NOT NULL,
			actor_display_name VARCHAR(100) NOT NULL,
			source_ip VARCHAR(45) CHARACTER SET ascii NOT NULL,
			user_agent VARCHAR(255) NOT NULL,
			action VARCHAR(96) CHARACTER SET ascii NOT NULL,
			resource_type VARCHAR(32) CHARACTER SET ascii NOT NULL,
			resource_id CHAR(32) CHARACTER SET ascii NOT NULL DEFAULT '',
			resource_name VARCHAR(100) NOT NULL DEFAULT '',
			risk_level VARCHAR(16) CHARACTER SET ascii NOT NULL,
			success BOOLEAN NOT NULL,
			error_code VARCHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			detail JSON NULL,
			KEY idx_audit_created (created_at),
			KEY idx_audit_actor (actor_user_id, created_at),
			KEY idx_audit_resource (resource_type, resource_id, created_at),
			KEY idx_audit_action (action, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS operator_state (
			id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
			panel_id CHAR(32) CHARACTER SET ascii NOT NULL,
			revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
			initialized BOOLEAN NOT NULL DEFAULT FALSE,
			updated_at DATETIME(6) NOT NULL,
			UNIQUE KEY uq_operator_state_panel_id (panel_id),
			CONSTRAINT chk_operator_state_singleton CHECK (id = 1)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`INSERT IGNORE INTO operator_state (id, panel_id, revision, initialized, updated_at)
			VALUES (1, LOWER(REPLACE(UUID(), '-', '')), 0, FALSE, CURRENT_TIMESTAMP(6))`,
		`CREATE TABLE IF NOT EXISTS operators (
			uuid CHAR(36) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			name VARCHAR(64) NOT NULL DEFAULT '',
			created_by_user_id CHAR(32) CHARACTER SET ascii NOT NULL,
			created_by_username VARCHAR(64) NOT NULL,
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			KEY idx_operators_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS file_operations (
			id CHAR(32) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			request_id CHAR(32) CHARACTER SET ascii NOT NULL,
			created_at DATETIME(6) NOT NULL,
			expires_at DATETIME(6) NOT NULL,
			completed_at DATETIME(6) NULL,
			actor_user_id CHAR(32) CHARACTER SET ascii NULL,
			session_id CHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			actor_username VARCHAR(64) NOT NULL,
			actor_display_name VARCHAR(100) NOT NULL,
			source_ip VARCHAR(45) CHARACTER SET ascii NOT NULL,
			user_agent VARCHAR(255) NOT NULL,
			action VARCHAR(96) CHARACTER SET ascii NOT NULL,
			node_id CHAR(32) CHARACTER SET ascii NOT NULL,
			resource_type VARCHAR(16) CHARACTER SET ascii NOT NULL,
			resource_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			status VARCHAR(16) CHARACTER SET ascii NOT NULL,
			error_code VARCHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			detail JSON NULL,
			KEY idx_file_operations_status (status, expires_at),
			KEY idx_file_operations_actor (actor_user_id, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS scheduled_tasks (
			id CHAR(32) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			enabled BOOLEAN NOT NULL,
			schedule_type VARCHAR(16) CHARACTER SET ascii NOT NULL,
			schedule_config JSON NOT NULL,
			timezone VARCHAR(64) CHARACTER SET ascii NOT NULL,
			action_type VARCHAR(16) CHARACTER SET ascii NOT NULL,
			action_payload TEXT NOT NULL,
			next_run_at DATETIME(6) NULL,
			last_run_at DATETIME(6) NULL,
			created_by_user_id CHAR(32) CHARACTER SET ascii NOT NULL,
			created_by_username VARCHAR(64) NOT NULL,
			created_by_display_name VARCHAR(100) NOT NULL,
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			UNIQUE KEY uq_scheduled_tasks_name (name),
			KEY idx_scheduled_tasks_due (enabled, next_run_at),
			KEY idx_scheduled_tasks_updated (updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS scheduled_task_targets (
			task_id CHAR(32) CHARACTER SET ascii NOT NULL,
			node_id CHAR(32) CHARACTER SET ascii NOT NULL,
			node_name VARCHAR(100) NOT NULL,
			server_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			instance_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			instance_name VARCHAR(100) NOT NULL,
			PRIMARY KEY (task_id, node_id, instance_id),
			KEY idx_scheduled_task_targets_node (node_id, instance_id),
			CONSTRAINT fk_scheduled_task_targets_task FOREIGN KEY (task_id) REFERENCES scheduled_tasks(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS task_runs (
			id CHAR(32) CHARACTER SET ascii NOT NULL PRIMARY KEY,
			scheduled_task_id CHAR(32) CHARACTER SET ascii NOT NULL,
			task_name VARCHAR(100) NOT NULL,
			trigger_type VARCHAR(16) CHARACTER SET ascii NOT NULL,
			status VARCHAR(32) CHARACTER SET ascii NOT NULL,
			action_type VARCHAR(16) CHARACTER SET ascii NOT NULL,
			action_payload TEXT NOT NULL,
			scheduled_for DATETIME(6) NULL,
			created_at DATETIME(6) NOT NULL,
			started_at DATETIME(6) NULL,
			finished_at DATETIME(6) NULL,
			total_targets INT UNSIGNED NOT NULL,
			success_targets INT UNSIGNED NOT NULL DEFAULT 0,
			failed_targets INT UNSIGNED NOT NULL DEFAULT 0,
			actor_user_id CHAR(32) CHARACTER SET ascii NOT NULL DEFAULT '',
			actor_username VARCHAR(64) NOT NULL DEFAULT '',
			actor_display_name VARCHAR(100) NOT NULL DEFAULT '',
			KEY idx_task_runs_created (created_at, id),
			KEY idx_task_runs_schedule (scheduled_task_id, created_at),
			KEY idx_task_runs_status (status, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS task_run_targets (
			run_id CHAR(32) CHARACTER SET ascii NOT NULL,
			node_id CHAR(32) CHARACTER SET ascii NOT NULL,
			node_name VARCHAR(100) NOT NULL,
			instance_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			instance_name VARCHAR(100) NOT NULL,
			status VARCHAR(16) CHARACTER SET ascii NOT NULL,
			started_at DATETIME(6) NULL,
			finished_at DATETIME(6) NULL,
			error_code VARCHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			error_message VARCHAR(500) NOT NULL DEFAULT '',
			PRIMARY KEY (run_id, node_id, instance_id),
			KEY idx_task_run_targets_status (status, started_at),
			CONSTRAINT fk_task_run_targets_run FOREIGN KEY (run_id) REFERENCES task_runs(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS plugin_artifacts_v2 (
			plugin_type VARCHAR(16) CHARACTER SET ascii NOT NULL,
			plugin_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			artifact_id BIGINT UNSIGNED NOT NULL,
			plugin_name VARCHAR(100) NOT NULL,
			version VARCHAR(100) NOT NULL,
			main_class VARCHAR(512) CHARACTER SET ascii NOT NULL DEFAULT '',
			jar_sha256 CHAR(64) CHARACTER SET ascii NOT NULL,
			config_sha256 CHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			current_artifact BOOLEAN NOT NULL DEFAULT FALSE,
			manifest JSON NOT NULL,
			uploaded_at DATETIME(6) NOT NULL,
			PRIMARY KEY (plugin_type, plugin_id, artifact_id),
			KEY idx_plugin_artifacts_type (plugin_type, current_artifact),
			KEY idx_plugin_artifacts_name (plugin_name),
			KEY idx_plugin_artifacts_version (version),
			KEY idx_plugin_artifacts_sha256 (jar_sha256),
			KEY idx_plugin_artifacts_current (current_artifact, plugin_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS proxy_sync_owners (
			proxy_node_id CHAR(32) CHARACTER SET ascii NOT NULL,
			proxy_server_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (proxy_node_id, proxy_server_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS proxy_sync_rules (
			proxy_node_id CHAR(32) CHARACTER SET ascii NOT NULL,
			proxy_server_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			target_node_id CHAR(32) CHARACTER SET ascii NOT NULL,
			target_server_id VARCHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			enabled BOOLEAN NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (proxy_node_id, proxy_server_id, target_node_id, target_server_id),
			KEY idx_proxy_sync_target (target_node_id, target_server_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS plugin_deploy_preferences (
			plugin_type VARCHAR(16) CHARACTER SET ascii NOT NULL,
			plugin_id VARCHAR(64) CHARACTER SET ascii NOT NULL,
			target_node_id CHAR(32) CHARACTER SET ascii NOT NULL,
			target_server_id VARCHAR(64) CHARACTER SET ascii NOT NULL DEFAULT '',
			enabled BOOLEAN NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (plugin_type, plugin_id, target_node_id, target_server_id),
			KEY idx_plugin_deploy_target (target_node_id, target_server_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id CHAR(32) CHARACTER SET ascii NOT NULL,
			namespace VARCHAR(32) CHARACTER SET ascii NOT NULL,
			schema_version SMALLINT UNSIGNED NOT NULL,
			settings JSON NOT NULL,
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (user_id, namespace),
			KEY idx_user_preferences_updated (updated_at),
			CONSTRAINT fk_user_preferences_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS mc_servers (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			game_id VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
			host VARCHAR(255) CHARACTER SET ascii NOT NULL,
			port SMALLINT UNSIGNED NOT NULL DEFAULT 25565,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			note VARCHAR(500) NOT NULL DEFAULT '',
			last_status VARCHAR(16) CHARACTER SET ascii NOT NULL DEFAULT 'unknown',
			last_online INT UNSIGNED NULL,
			last_max INT UNSIGNED NULL,
			last_latency_ms INT UNSIGNED NULL,
			last_version VARCHAR(100) NOT NULL DEFAULT '',
			last_error VARCHAR(500) NOT NULL DEFAULT '',
			last_checked_at DATETIME(6) NULL,
			created_at DATETIME(6) NOT NULL,
			updated_at DATETIME(6) NOT NULL,
			UNIQUE KEY uq_mc_servers_game_id (game_id),
			KEY idx_mc_servers_enabled (enabled)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS mc_server_observations (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			server_id BIGINT UNSIGNED NOT NULL,
			sampled_at DATETIME(6) NOT NULL,
			online INT UNSIGNED NOT NULL,
			max_players INT UNSIGNED NOT NULL,
			latency_ms INT UNSIGNED NOT NULL,
			version_name VARCHAR(100) NOT NULL DEFAULT '',
			protocol INT NOT NULL DEFAULT 0,
			KEY idx_mc_server_observations_server_time (server_id, sampled_at),
			KEY idx_mc_server_observations_time (sampled_at),
			CONSTRAINT fk_mc_server_observations_server FOREIGN KEY (server_id) REFERENCES mc_servers(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize schema: %w", err)
		}
	}
	return nil
}
