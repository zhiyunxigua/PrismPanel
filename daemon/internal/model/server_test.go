package model

import (
	"path/filepath"
	"testing"
)

func TestMirrorInstances(t *testing.T) {
	cfg := ServerConfig{
		SchemaVersion: 1, Type: "mirror", ServerID: "bedwars", Name: "BedWars",
		RootPath: filepath.Join(t.TempDir(), "bedwars"), ImageDirectory: "image", InstanceCount: 2,
		Ports:   []int{25571, 25572},
		Process: ProcessConfig{StartCommand: "java -jar server.jar nogui", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: ConsoleConfig{Encoding: "utf-8"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	instances := cfg.Instances()
	if len(instances) != 2 || instances[0].InstanceID != "bedwars_1" || instances[1].Port != 25572 {
		t.Fatalf("unexpected derived instances: %#v", instances)
	}
}

func TestMissingStartCommandIsRejected(t *testing.T) {
	cfg := ServerConfig{
		SchemaVersion: 1, Type: "standalone", ServerID: "legacy", Name: "Legacy",
		Workspace: t.TempDir(), Port: 25565,
		Process: ProcessConfig{StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: ConsoleConfig{Encoding: "utf-8"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected configuration without start_command to be rejected")
	}
}
