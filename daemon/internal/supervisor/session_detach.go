package supervisor

func (m *Manager) DetachRunningSessions() {
	m.closing.Store(true)
	m.mu.RLock()
	instances := make([]*instance, 0, len(m.instances))
	for _, current := range m.instances {
		instances = append(instances, current)
	}
	m.mu.RUnlock()
	for _, current := range instances {
		current.mu.Lock()
		client := current.session
		current.session = nil
		current.mu.Unlock()
		if client != nil {
			client.markDetached()
		}
	}
}
