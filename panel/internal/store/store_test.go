package store

import (
	"strings"
	"testing"
)

func TestPrefixStatement(t *testing.T) {
	query := prefixStatement(
		"SELECT users.id FROM users JOIN user_groups ON users.group_code = user_groups.code",
		"prism_",
	)
	for _, table := range []string{"prism_users", "prism_user_groups"} {
		if !strings.Contains(query, "`"+table+"`") {
			t.Fatalf("missing prefixed table %q in %q", table, query)
		}
	}
	constraint := prefixStatement(
		"CONSTRAINT fk_user_permission_overrides_user FOREIGN KEY (user_id) REFERENCES users(id)",
		"prism_",
	)
	if !strings.Contains(constraint, "fk_user_permission_overrides_user") {
		t.Fatalf("rewrote constraint name: %q", constraint)
	}
	pluginQuery := prefixStatement("DELETE FROM plugin_artifacts", "prism_")
	if !strings.Contains(pluginQuery, "`prism_plugin_artifacts`") {
		t.Fatalf("missing prefixed plugin table in %q", pluginQuery)
	}
	fileOperationQuery := prefixStatement("UPDATE file_operations SET status = 'failed'", "prism_")
	if !strings.Contains(fileOperationQuery, "`prism_file_operations`") {
		t.Fatalf("missing prefixed file operation table in %q", fileOperationQuery)
	}
}

func TestPrefixStatementNewPluginAndSelectionTables(t *testing.T) {
	for _, table := range []string{
		`plugin_artifacts_v2`,
		`proxy_sync_owners`,
		`proxy_sync_rules`,
		`plugin_deploy_preferences`,
		`user_preferences`,
		`net_games`,
		`net_game_collection_runs`,
		`net_game_observations`,
	} {
		statement := prefixStatement(`DELETE FROM `+table, `prism_`)
		quotedTable := string(rune(96)) + `prism_` + table + string(rune(96))
		if !strings.Contains(statement, quotedTable) {
			t.Fatalf(`missing prefixed table %q in %q`, table, statement)
		}
	}
}
