package supervisor

import (
	"sort"
	"sync"

	"PrismPanel-daemon/internal/apperr"
)

func (m *Manager) ReserveDeployment(instanceIDs []string) (func(), error) {
	ids := append([]string(nil), instanceIDs...)
	sort.Strings(ids)
	items := make([]*instance, 0, len(ids))
	for _, id := range ids {
		current, err := m.lookup(id)
		if err != nil {
			unlockOperations(items)
			return nil, err
		}
		if !current.op.TryLock() {
			unlockOperations(items)
			return nil, apperr.New("INSTANCE_BUSY", "部署目标正在执行其他操作")
		}
		items = append(items, current)
		current.mu.RLock()
		locked := current.deploymentLocked
		state := current.state
		current.mu.RUnlock()
		if locked || state == StateStarting || state == StateStopping || state == StateDeploying {
			unlockOperations(items)
			return nil, apperr.New("INSTANCE_BUSY", "部署目标正在执行其他操作")
		}
	}
	for _, current := range items {
		current.mu.Lock()
		current.deploymentLocked = true
		current.mu.Unlock()
	}
	unlockOperations(items)
	for _, current := range items {
		m.publishState(current)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			for _, current := range items {
				current.op.Lock()
				current.mu.Lock()
				current.deploymentLocked = false
				current.mu.Unlock()
				current.op.Unlock()
				m.publishState(current)
			}
		})
	}, nil
}

func unlockOperations(items []*instance) {
	for index := len(items) - 1; index >= 0; index-- {
		items[index].op.Unlock()
	}
}

func deploymentLockError(current *instance) error {
	current.mu.RLock()
	locked := current.deploymentLocked
	current.mu.RUnlock()
	if locked {
		return apperr.New("INSTANCE_BUSY", "实例已被部署任务锁定")
	}
	return nil
}
