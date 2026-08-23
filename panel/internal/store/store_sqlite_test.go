package store

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSQLiteSchema 验证 MySQL 语法翻译后的建表语句能在 SQLite 上执行。
func TestSQLiteSchema(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer raw.Close()
	store := &Store{db: &database{DB: raw, prefix: "prism_", sqlite: true}}
	if err := store.initializeSchema(context.Background()); err != nil {
		t.Fatalf("initialize schema on sqlite: %v", err)
	}
}

// TestSQLiteTranslation 抽查关键 MySQL 语法的翻译结果。
func TestSQLiteTranslation(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			in:   "INSERT IGNORE INTO user_groups (code) VALUES (1) ON DUPLICATE KEY UPDATE name = VALUES(name)",
			want: "INSERT OR IGNORE INTO user_groups (code) VALUES (1) ON CONFLICT DO UPDATE SET name = excluded.name",
		},
		{
			in:   "SELECT built_in FROM user_groups WHERE code = ? FOR UPDATE",
			want: "SELECT built_in FROM user_groups WHERE code = ?",
		},
		{
			in:   "INSERT INTO t (id) VALUES (1) ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)",
			want: "INSERT INTO t (id) VALUES (1) ON CONFLICT DO UPDATE SET id = last_insert_rowid()",
		},
		{
			in:   "UPDATE t SET at = CURRENT_TIMESTAMP(6)",
			want: "UPDATE t SET at = CURRENT_TIMESTAMP",
		},
	}
	for _, c := range cases {
		if got := toSQLite(c.in); got != c.want {
			t.Errorf("toSQLite(%q)\n got: %s\nwant: %s", c.in, got, c.want)
		}
	}
}
