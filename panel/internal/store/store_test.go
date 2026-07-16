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
}
