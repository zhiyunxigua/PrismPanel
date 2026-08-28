package metrics

import (
	"sort"
	"sync"
	"time"
)

const (
	retention        = 10 * time.Minute
	maxHistoryPoints = 120
)

type HostSnapshot struct {
	SampledAt            time.Time `json:"sampled_at"`
	CPUPercent           float64   `json:"cpu_percent"`
	CPUCorePercent       []float64 `json:"cpu_core_percent"`
	MemoryTotalBytes     uint64    `json:"memory_total_bytes"`
	MemoryUsedBytes      uint64    `json:"memory_used_bytes"`
	MemoryAvailableBytes uint64    `json:"memory_available_bytes"`
	MemoryPercent        float64   `json:"memory_percent"`
}

type InstanceSnapshot struct {
	InstanceID    string   `json:"instance_id"`
	ServerID      string   `json:"server_id"`
	Platform      string   `json:"platform"`
	Name          string   `json:"name"`
	State         string   `json:"state"`
	CPUPercent    *float64 `json:"cpu_percent,omitempty"`
	MemoryBytes   *uint64  `json:"memory_bytes,omitempty"`
	OnlinePlayers *int     `json:"online_players,omitempty"`
	TPS           *float64 `json:"tps,omitempty"`
}

type Snapshot struct {
	Host      HostSnapshot       `json:"host"`
	Instances []InstanceSnapshot `json:"instances"`
}

type InstancePoint struct {
	SampledAt     time.Time `json:"sampled_at"`
	State         string    `json:"state"`
	CPUPercent    *float64  `json:"cpu_percent,omitempty"`
	MemoryBytes   *uint64   `json:"memory_bytes,omitempty"`
	OnlinePlayers *int      `json:"online_players,omitempty"`
	TPS           *float64  `json:"tps,omitempty"`
}

type InstanceSeries struct {
	InstanceID string          `json:"instance_id"`
	ServerID   string          `json:"server_id"`
	Platform   string          `json:"platform"`
	Name       string          `json:"name"`
	Points     []InstancePoint `json:"points"`
}

type InstanceCurrent struct {
	InstanceID    string   `json:"instance_id"`
	ServerID      string   `json:"server_id"`
	Platform      string   `json:"platform"`
	Name          string   `json:"name"`
	State         string   `json:"state"`
	CPUPercent    *float64 `json:"cpu_percent,omitempty"`
	MemoryBytes   *uint64  `json:"memory_bytes,omitempty"`
	OnlinePlayers *int     `json:"online_players,omitempty"`
	TPS           *float64 `json:"tps,omitempty"`
}

type NodeCurrent struct {
	Host      *HostSnapshot     `json:"host,omitempty"`
	Instances []InstanceCurrent `json:"instances"`
}

type instanceKey struct {
	nodeID     string
	instanceID string
}

type instanceHistory struct {
	serverID string
	platform string
	name     string
	points   []InstancePoint
}

type Store struct {
	mu        sync.RWMutex
	hosts     map[string][]HostSnapshot
	instances map[instanceKey]instanceHistory
}

func NewStore() *Store {
	return &Store{
		hosts: make(map[string][]HostSnapshot), instances: make(map[instanceKey]instanceHistory),
	}
}

func (s *Store) Record(nodeID string, snapshot Snapshot) {
	if nodeID == "" {
		return
	}
	sampledAt := snapshot.Host.SampledAt
	if sampledAt.IsZero() {
		sampledAt = time.Now().UTC()
	}
	cutoff := sampledAt.Add(-retention)
	s.mu.Lock()
	host := snapshot.Host
	host.SampledAt = sampledAt
	host.CPUCorePercent = append([]float64(nil), host.CPUCorePercent...)
	s.hosts[nodeID] = appendHostPoint(s.hosts[nodeID], host, cutoff)
	for _, instance := range snapshot.Instances {
		if instance.InstanceID == "" {
			continue
		}
		key := instanceKey{nodeID: nodeID, instanceID: instance.InstanceID}
		history := s.instances[key]
		history.serverID = instance.ServerID
		history.platform = instance.Platform
		history.name = instance.Name
		history.points = appendInstancePoint(history.points, InstancePoint{
			SampledAt: sampledAt, State: instance.State,
			CPUPercent: copyFloat64(instance.CPUPercent), MemoryBytes: copyUint64(instance.MemoryBytes),
			OnlinePlayers: copyInt(instance.OnlinePlayers), TPS: copyFloat64(instance.TPS),
		}, cutoff)
		s.instances[key] = history
	}
	for key, history := range s.instances {
		if key.nodeID != nodeID {
			continue
		}
		history.points = pruneInstancePoints(history.points, cutoff)
		if len(history.points) == 0 {
			delete(s.instances, key)
		} else {
			s.instances[key] = history
		}
	}
	s.mu.Unlock()
}

func (s *Store) RemoveNode(nodeID string) {
	s.mu.Lock()
	delete(s.hosts, nodeID)
	for key := range s.instances {
		if key.nodeID == nodeID {
			delete(s.instances, key)
		}
	}
	s.mu.Unlock()
}

func (s *Store) NodeHistory(nodeID string) []HostSnapshot {
	s.mu.RLock()
	source := s.hosts[nodeID]
	cutoff := time.Now().UTC().Add(-retention)
	first := 0
	for first < len(source) && source[first].SampledAt.Before(cutoff) {
		first++
	}
	result := make([]HostSnapshot, len(source)-first)
	for index, point := range source[first:] {
		result[index] = point
		result[index].CPUCorePercent = append([]float64(nil), point.CPUCorePercent...)
	}
	s.mu.RUnlock()
	return result
}

func (s *Store) ServerHistory(nodeID, serverID string) []InstanceSeries {
	s.mu.RLock()
	cutoff := time.Now().UTC().Add(-retention)
	result := make([]InstanceSeries, 0)
	for key, history := range s.instances {
		if key.nodeID != nodeID || history.serverID != serverID {
			continue
		}
		first := 0
		for first < len(history.points) && history.points[first].SampledAt.Before(cutoff) {
			first++
		}
		if first == len(history.points) {
			continue
		}
		result = append(result, InstanceSeries{
			InstanceID: key.instanceID, ServerID: history.serverID, Platform: history.platform, Name: history.name,
			Points: append([]InstancePoint(nil), history.points[first:]...),
		})
	}
	s.mu.RUnlock()
	sort.Slice(result, func(left, right int) bool { return result[left].InstanceID < result[right].InstanceID })
	return result
}

func (s *Store) CurrentNode(nodeID string) NodeCurrent {
	cutoff := time.Now().UTC().Add(-retention)
	s.mu.RLock()
	result := NodeCurrent{Instances: make([]InstanceCurrent, 0)}
	if hosts := s.hosts[nodeID]; len(hosts) > 0 {
		latest := hosts[len(hosts)-1]
		if !latest.SampledAt.Before(cutoff) {
			latest.CPUCorePercent = append([]float64(nil), latest.CPUCorePercent...)
			result.Host = &latest
		}
	}
	for key, history := range s.instances {
		if key.nodeID != nodeID || len(history.points) == 0 {
			continue
		}
		latest := history.points[len(history.points)-1]
		if latest.SampledAt.Before(cutoff) {
			continue
		}
		result.Instances = append(result.Instances, InstanceCurrent{
			InstanceID: key.instanceID, ServerID: history.serverID, Platform: history.platform,
			Name: history.name, State: latest.State,
			CPUPercent: copyFloat64(latest.CPUPercent), MemoryBytes: copyUint64(latest.MemoryBytes),
			OnlinePlayers: copyInt(latest.OnlinePlayers), TPS: copyFloat64(latest.TPS),
		})
	}
	s.mu.RUnlock()
	sort.Slice(result.Instances, func(left, right int) bool {
		return result.Instances[left].InstanceID < result.Instances[right].InstanceID
	})
	return result
}

func appendHostPoint(points []HostSnapshot, point HostSnapshot, cutoff time.Time) []HostSnapshot {
	if len(points) > 0 && points[len(points)-1].SampledAt.Equal(point.SampledAt) {
		points[len(points)-1] = point
	} else {
		points = append(points, point)
	}
	first := 0
	for first < len(points) && points[first].SampledAt.Before(cutoff) {
		first++
	}
	points = points[first:]
	if len(points) > maxHistoryPoints {
		points = points[len(points)-maxHistoryPoints:]
	}
	return points
}

func appendInstancePoint(points []InstancePoint, point InstancePoint, cutoff time.Time) []InstancePoint {
	if len(points) > 0 && points[len(points)-1].SampledAt.Equal(point.SampledAt) {
		points[len(points)-1] = point
	} else {
		points = append(points, point)
	}
	points = pruneInstancePoints(points, cutoff)
	if len(points) > maxHistoryPoints {
		points = points[len(points)-maxHistoryPoints:]
	}
	return points
}

func pruneInstancePoints(points []InstancePoint, cutoff time.Time) []InstancePoint {
	first := 0
	for first < len(points) && points[first].SampledAt.Before(cutoff) {
		first++
	}
	return points[first:]
}

func copyFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
