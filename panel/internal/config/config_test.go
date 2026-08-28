package config

import (
	"testing"

	"github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"
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

func TestDefaultManagesOperators(t *testing.T) {
	if !Default().Minecraft.ManageOperators {
		t.Fatal("operator management should default to enabled")
	}
}

func TestConfigCanDisableOperatorManagement(t *testing.T) {
	cfg := Default()
	if err := yaml.Unmarshal([]byte("minecraft:\n  manage_operators: false\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Minecraft.ManageOperators {
		t.Fatal("operator management should respect an explicit false value")
	}
}

func TestMailFeatureDefaultsDisabled(t *testing.T) {
	if Default().Features.Mail {
		t.Fatal("mail feature should default to disabled")
	}
}

func TestMailFeatureRequiresPlayerDataCredentials(t *testing.T) {
	cfg := Default()
	cfg.Features.Mail = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected enabled mail feature to require PlayerData credentials")
	}
}
