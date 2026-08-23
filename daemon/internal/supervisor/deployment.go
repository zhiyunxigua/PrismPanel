package supervisor

import (
	"errors"

	"PrismPanel-daemon/internal/apperr"
)

type DeploymentTarget struct {
	InstanceID string
	Workspace  string
	Port       int
	WasRunning bool
}

// DeployInstance serializes a deployment with all lifecycle operations for an instance.
func (m *Manager) DeployInstance(
	instanceID string,
	apply func(DeploymentTarget) error,
	shouldRestore func() bool,
	wasCancelled func() bool,
) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "实例正在执行其他操作")
	}
	defer current.op.Unlock()

	current.mu.RLock()
	state := current.state
	wasRunning := current.cmd != nil && state == StateRunning
	current.mu.RUnlock()
	if state == StateStarting || state == StateStopping || state == StateDeploying {
		return apperr.New("INSTANCE_BUSY", "实例正在切换状态")
	}
	if wasRunning {
		if err := m.stopLocked(current); err != nil {
			return err
		}
	}

	current.mu.Lock()
	current.state = StateDeploying
	current.lastError = ""
	target := DeploymentTarget{
		InstanceID: current.cfg.InstanceID,
		Workspace:  current.cfg.Workspace,
		Port:       current.cfg.Port,
		WasRunning: wasRunning,
	}
	current.mu.Unlock()
	m.publishState(current)

	applyErr := apply(target)
	current.mu.Lock()
	if applyErr != nil && !wasCancelled() {
		current.state = StateFailed
		current.lastError = applyErr.Error()
	} else {
		current.state = StateStopped
		current.lastError = ""
	}
	current.mu.Unlock()
	m.publishState(current)

	if wasRunning && shouldRestore() {
		startErr := m.startLocked(current)
		if startErr != nil {
			if applyErr != nil {
				return errors.Join(applyErr, startErr)
			}
			return startErr
		}
	}
	return applyErr
}

func WriteServerPort(workspace string, port int) error {
	return writeServerPort(workspace, port)
}
