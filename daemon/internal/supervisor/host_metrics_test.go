package supervisor

import "testing"

func TestAveragePercent(t *testing.T) {
	if got := averagePercent([]float64{10, 20, 30, 40}); got != 25 {
		t.Fatalf("averagePercent() = %v, want 25", got)
	}
	if got := averagePercent(nil); got != 0 {
		t.Fatalf("averagePercent(nil) = %v, want 0", got)
	}
}

func TestCloneHostSnapshotCopiesCoreMetrics(t *testing.T) {
	source := HostSnapshot{CPUCorePercent: []float64{10, 20}}
	cloned := cloneHostSnapshot(source)
	cloned.CPUCorePercent[0] = 99
	if source.CPUCorePercent[0] != 10 {
		t.Fatal("cloneHostSnapshot shared CPUCorePercent storage")
	}
}
