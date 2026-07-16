package supervisor

import (
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func (m *Manager) sampleProcessMetrics(current *instance, rootPID int, sessionID string, done <-chan struct{}) {
	known := make(map[int32]*process.Process)
	logicalCPUs := runtime.NumCPU()
	sample := func() {
		targets := processTree(int32(rootPID), known)
		var cpuTotal float64
		var memoryTotal uint64
		cpuAvailable := false
		memoryAvailable := false
		for _, target := range targets {
			if cpu, err := target.Percent(0); err == nil {
				cpuTotal += cpu
				cpuAvailable = true
			}
			if memoryInfo, err := target.MemoryInfo(); err == nil && memoryInfo != nil {
				memoryTotal += memoryInfo.RSS
				memoryAvailable = true
			}
		}
		current.mu.Lock()
		defer current.mu.Unlock()
		if current.pid != rootPID || current.sessionID != sessionID {
			return
		}
		if cpuAvailable {
			value := taskManagerCPUPercent(cpuTotal, logicalCPUs)
			current.cpuPercent = &value
		}
		if memoryAvailable {
			value := memoryTotal
			current.memoryBytes = &value
		}
	}
	sample()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			sample()
		}
	}
}

func taskManagerCPUPercent(coreRelativePercent float64, logicalCPUs int) float64 {
	if coreRelativePercent <= 0 || logicalCPUs <= 0 {
		return 0
	}
	value := coreRelativePercent / float64(logicalCPUs)
	if value > 100 {
		return 100
	}
	return value
}

func processTree(rootPID int32, known map[int32]*process.Process) []*process.Process {
	root, exists := known[rootPID]
	if !exists {
		var err error
		root, err = process.NewProcess(rootPID)
		if err != nil {
			return nil
		}
		known[rootPID] = root
	}
	queue := []*process.Process{root}
	result := make([]*process.Process, 0, 2)
	seen := make(map[int32]struct{})
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, exists := seen[current.Pid]; exists {
			continue
		}
		seen[current.Pid] = struct{}{}
		result = append(result, current)
		children, err := current.Children()
		if err != nil {
			continue
		}
		for _, child := range children {
			if existing, exists := known[child.Pid]; exists {
				queue = append(queue, existing)
			} else {
				known[child.Pid] = child
				queue = append(queue, child)
			}
		}
	}
	return result
}

func processBelongsToTree(candidatePID, rootPID int) bool {
	if candidatePID <= 0 || rootPID <= 0 {
		return false
	}
	if candidatePID == rootPID {
		return true
	}
	candidate, err := process.NewProcess(int32(candidatePID))
	if err != nil {
		return false
	}
	for depth := 0; depth < 64; depth++ {
		parentPID, err := candidate.Ppid()
		if err != nil || parentPID <= 0 {
			return false
		}
		if int(parentPID) == rootPID {
			return true
		}
		candidate, err = process.NewProcess(parentPID)
		if err != nil {
			return false
		}
	}
	return false
}
