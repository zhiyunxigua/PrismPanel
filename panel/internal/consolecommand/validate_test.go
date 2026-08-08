package consolecommand

import "testing"

func TestValidateOperatorManagement(t *testing.T) {
	for _, command := range []string{"op Steve", "/deop Steve", "minecraft:op Steve", "/minecraft:deop Steve"} {
		if err := Validate(command, true); err == nil {
			t.Fatalf("expected %q to be rejected", command)
		}
		if err := Validate(command, false); err != nil {
			t.Fatalf("expected %q to be allowed when management is disabled: %v", command, err)
		}
	}
}

func TestValidateRegularCommand(t *testing.T) {
	for _, command := range []string{"say hello", "list", "", "whitelist on"} {
		if err := Validate(command, true); err != nil {
			t.Fatalf("expected %q to be allowed: %v", command, err)
		}
	}
}
