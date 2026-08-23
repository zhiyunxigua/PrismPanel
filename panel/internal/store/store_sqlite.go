package store

import (
	"regexp"
	"strings"
)

// toSQLite 将 MySQL 语法翻译为 SQLite 语法（本地测试回退用）。
// 仅处理本项目 store 中实际出现的 MySQL 特性，SQLite 与 MySQL 共有的语法保持不变。

var (
	sqliteEngineSuffix  = regexp.MustCompile(`(?i)\s+ENGINE\s*=\s*InnoDB\s+DEFAULT\s+CHARSET\s*=\s*\w+(?:\s+COLLATE\s*=\s*\w+)?`)
	sqliteCharacterSet  = regexp.MustCompile(`(?i)\s+CHARACTER\s+SET\s+\w+`)
	sqliteCollate       = regexp.MustCompile(`(?i)\s+COLLATE\s+\w+`)
	sqliteDatetimePrec  = regexp.MustCompile(`(?i)DATETIME\(\d+\)`)
	sqliteUniqueKey     = regexp.MustCompile(`(?i)UNIQUE\s+KEY\s+(\w+)\s*\(`)
	sqlitePlainKeyComma = regexp.MustCompile(`(?i),\s*KEY\s+\w+\s*\([^)]*\)`)
	sqlitePlainKey      = regexp.MustCompile(`(?i)\s*KEY\s+\w+\s*\([^)]*\)`)
	sqliteAutoIncBig    = regexp.MustCompile(`(?i)BIGINT\s+UNSIGNED\s+NOT\s+NULL\s+AUTO_INCREMENT\s+PRIMARY\s+KEY`)
	sqliteAutoIncBig2   = regexp.MustCompile(`(?i)BIGINT\s+AUTO_INCREMENT\s+PRIMARY\s+KEY`)
	sqliteAutoInc       = regexp.MustCompile(`(?i)AUTO_INCREMENT`)
	sqliteInsertIgnore  = regexp.MustCompile(`(?i)INSERT\s+IGNORE\s+INTO`)
	sqliteCurrentTs     = regexp.MustCompile(`(?i)CURRENT_TIMESTAMP\(\d+\)`)
	sqliteUUID          = regexp.MustCompile(`(?i)UUID\(\s*\)`)
	sqliteForUpdate     = regexp.MustCompile(`(?i)\s+FOR\s+UPDATE`)
	sqliteLastInsertID  = regexp.MustCompile(`(?i)LAST_INSERT_ID\(id\)`)
	sqliteDuplicateKey  = regexp.MustCompile(`(?i)ON\s+DUPLICATE\s+KEY\s+UPDATE`)
	sqliteValuesFunc    = regexp.MustCompile(`(?i)VALUES\(\s*([\w.]+)\s*\)`)
	sqliteUnsigned      = regexp.MustCompile(`(?i)\bUNSIGNED\b`)
	sqliteBooleanType   = regexp.MustCompile(`(?i)\bBOOLEAN\b`)
	sqliteJsonType      = regexp.MustCompile(`(?i)\bJSON\b`)
)

func toSQLite(query string) string {
	query = sqliteEngineSuffix.ReplaceAllString(query, "")
	query = sqliteCharacterSet.ReplaceAllString(query, "")
	query = sqliteCollate.ReplaceAllString(query, "")
	query = sqliteDatetimePrec.ReplaceAllString(query, "DATETIME")
	query = sqliteUniqueKey.ReplaceAllString(query, "CONSTRAINT $1 UNIQUE (")
	query = sqlitePlainKeyComma.ReplaceAllString(query, "")
	query = sqlitePlainKey.ReplaceAllString(query, "")
	query = sqliteAutoIncBig.ReplaceAllString(query, "INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT")
	query = sqliteAutoIncBig2.ReplaceAllString(query, "INTEGER PRIMARY KEY AUTOINCREMENT")
	query = sqliteAutoInc.ReplaceAllString(query, "AUTOINCREMENT")
	query = sqliteInsertIgnore.ReplaceAllString(query, "INSERT OR IGNORE INTO")
	query = sqliteCurrentTs.ReplaceAllString(query, "CURRENT_TIMESTAMP")
	query = sqliteUUID.ReplaceAllString(query, "hex(randomblob(16))")
	query = sqliteForUpdate.ReplaceAllString(query, "")
	query = sqliteUnsigned.ReplaceAllString(query, "")
	query = sqliteBooleanType.ReplaceAllString(query, "INTEGER")
	query = sqliteJsonType.ReplaceAllString(query, "TEXT")
	return sqliteUpsert(query)
}

// sqliteUpsert 把 MySQL 的 ON DUPLICATE KEY UPDATE 翻译为 SQLite 的
// ON CONFLICT DO UPDATE SET，并把 SET 子句中的 VALUES(col) 改为 excluded.col。
func sqliteUpsert(query string) string {
	index := sqliteDuplicateKey.FindStringIndex(query)
	if index == nil {
		return query
	}
	head := strings.TrimRight(query[:index[0]], " ")
	tail := strings.TrimLeft(query[index[1]:], " ")
	tail = sqliteValuesFunc.ReplaceAllString(tail, "excluded.$1")
	tail = sqliteLastInsertID.ReplaceAllString(tail, "last_insert_rowid()")
	return head + " ON CONFLICT DO UPDATE SET " + tail
}
