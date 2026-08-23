package supervisor

import "testing"

func TestTaskManagerCPUPercent(t *testing.T) {
	tests := []struct {
		name        string
		corePercent float64
		logicalCPUs int
		want        float64
	}{
		{name: "two of eight cores", corePercent: 200, logicalCPUs: 8, want: 25},
		{name: "one of four cores", corePercent: 100, logicalCPUs: 4, want: 25},
		{name: "clamps sampling jitter", corePercent: 900, logicalCPUs: 8, want: 100},
		{name: "invalid core count", corePercent: 100, logicalCPUs: 0, want: 0},
		{name: "negative sample", corePercent: -1, logicalCPUs: 8, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := taskManagerCPUPercent(test.corePercent, test.logicalCPUs); got != test.want {
				t.Fatalf("taskManagerCPUPercent(%v, %d) = %v, want %v", test.corePercent, test.logicalCPUs, got, test.want)
			}
		})
	}
}
