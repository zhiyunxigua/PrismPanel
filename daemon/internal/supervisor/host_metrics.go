package supervisor

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

const metricsSampleInterval = 5 * time.Second

type HostSnapshot struct {
	SampledAt            time.Time `json:"sampled_at"`
	CPUPercent           float64   `json:"cpu_percent"`
	CPUCorePercent       []float64 `json:"cpu_core_percent"`
	MemoryTotalBytes     uint64    `json:"memory_total_bytes"`
	MemoryUsedBytes      uint64    `json:"memory_used_bytes"`
	MemoryAvailableBytes uint64    `json:"memory_available_bytes"`
	MemoryPercent        float64   `json:"memory_percent"`
}

type MetricsSnapshot struct {
	Host      HostSnapshot `json:"host"`
	Instances []Snapshot   `json:"instances"`
}

func (m *Manager) StartMetrics(ctx context.Context) {
	m.hostOnce.Do(func() {
		m.sampleHostMetrics()
		go func() {
			ticker := time.NewTicker(metricsSampleInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					m.sampleHostMetrics()
				}
			}
		}()
	})
}

func (m *Manager) MetricsSnapshot() MetricsSnapshot {
	m.hostMu.RLock()
	host := cloneHostSnapshot(m.host)
	m.hostMu.RUnlock()
	return MetricsSnapshot{Host: host, Instances: m.List()}
}

func (m *Manager) sampleHostMetrics() {
	cores, cpuErr := cpu.Percent(0, true)
	memory, memoryErr := mem.VirtualMemory()
	if cpuErr != nil && memoryErr != nil {
		return
	}
	m.hostMu.Lock()
	next := cloneHostSnapshot(m.host)
	next.SampledAt = time.Now().UTC()
	if cpuErr == nil && len(cores) > 0 {
		next.CPUCorePercent = append(next.CPUCorePercent[:0], cores...)
		next.CPUPercent = averagePercent(cores)
	}
	if memoryErr == nil && memory != nil {
		next.MemoryTotalBytes = memory.Total
		next.MemoryUsedBytes = memory.Used
		next.MemoryAvailableBytes = memory.Available
		next.MemoryPercent = memory.UsedPercent
	}
	m.host = next
	m.hostMu.Unlock()
}

func averagePercent(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func cloneHostSnapshot(source HostSnapshot) HostSnapshot {
	copy := source
	copy.CPUCorePercent = append([]float64(nil), source.CPUCorePercent...)
	return copy
}
