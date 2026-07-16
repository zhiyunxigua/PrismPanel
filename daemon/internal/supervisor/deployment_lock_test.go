package supervisor

import (
	"testing"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
)

func TestDeploymentReservationBlocksInstanceOperations(t *testing.T) {
	server := model.ServerConfig{
		SchemaVersion: 1, Type: "mirror", ServerID: "test", Name: "Test",
		RootPath: t.TempDir(), ImageDirectory: "image", InstanceCount: 1, Ports: []int{25565},
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	release, err := manager.ReserveDeployment([]string{"test_1"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Get("test_1")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.DeploymentLocked {
		t.Fatal("deployment reservation was not exposed in the snapshot")
	}
	if err := manager.Start("test_1"); apperr.From(err).Code != "INSTANCE_BUSY" {
		t.Fatalf("start during deployment returned %v", err)
	}
	if err := manager.Command("test_1", "say test"); apperr.From(err).Code != "INSTANCE_BUSY" {
		t.Fatalf("command during deployment returned %v", err)
	}
	release()
	snapshot, _ = manager.Get("test_1")
	if snapshot.DeploymentLocked {
		t.Fatal("deployment reservation was not released")
	}
	secondRelease, err := manager.ReserveDeployment([]string{"test_1"})
	if err != nil {
		t.Fatalf("instance could not be reserved again: %v", err)
	}
	secondRelease()
}
