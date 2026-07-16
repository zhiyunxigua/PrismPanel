package api

import (
	"encoding/json"
	"testing"
)

func TestValidateConsoleCommandRejectsOPChanges(t *testing.T) {
	for _, command := range []string{"op Steve", "/deop Steve", "minecraft:op Steve", "/minecraft:deop Steve"} {
		if err := validateConsoleCommand(command); err == nil {
			t.Fatalf("expected %q to be rejected", command)
		}
	}
}

func TestValidateConsoleCommandAllowsRegularCommands(t *testing.T) {
	for _, command := range []string{"say hello", "list", "", "whitelist on"} {
		if err := validateConsoleCommand(command); err != nil {
			t.Fatalf("expected %q to be allowed: %v", command, err)
		}
	}
}

func TestSanitizeInstanceMessagesProtectsPlayerAndPluginDetails(t *testing.T) {
	input := []json.RawMessage{json.RawMessage(`{
		"instance_id":"test","online_players":1,
		"players":[{"uuid":"player","name":"Steve"}],
		"plugins":[{"name":"PrismMC"}]
	}`)}
	result := sanitizeInstanceMessages(input, false, false)
	var item map[string]any
	if err := json.Unmarshal(result[0], &item); err != nil {
		t.Fatal(err)
	}
	if _, exists := item["players"]; exists {
		t.Fatal("expected player details to be removed")
	}
	if _, exists := item["plugins"]; exists {
		t.Fatal("expected plugin details to be removed")
	}
	if item["online_players"] != float64(1) {
		t.Fatal("expected aggregate player count to remain available")
	}
}
