package config

import (
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestDatabaseConfigDSN(t *testing.T) {
	input := DatabaseConfig{
		URL: "127.0.0.1:3306", Username: "root", Password: "p@ss:/word",
		Name: "prismpanel", TablePrefix: "prism_", MaxOpenConns: 10, MaxIdleConns: 5,
	}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	parsed, err := mysql.ParseDSN(input.DSN())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Addr != input.URL || parsed.User != input.Username || parsed.Passwd != input.Password ||
		parsed.DBName != input.Name || !parsed.ParseTime {
		t.Fatalf("unexpected DSN config: %#v", parsed)
	}
}

func TestDatabaseConfigRejectsUnsafePrefix(t *testing.T) {
	input := DatabaseConfig{
		URL: "127.0.0.1:3306", Username: "root", Name: "prismpanel",
		TablePrefix: "prism_;DROP_TABLE", MaxOpenConns: 10, MaxIdleConns: 5,
	}
	if err := input.Validate(); err == nil {
		t.Fatal("accepted an unsafe table prefix")
	}
}
