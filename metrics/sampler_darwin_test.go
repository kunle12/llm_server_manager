//go:build darwin

package metrics

import "testing"

func TestParseTopCPU(t *testing.T) {
	out := `Processes: 500 total
CPU usage: 5.64% user, 10.29% sys, 84.5% idle
PhysMem: 14G used`
	usage, ok := parseTopCPU(out)
	if !ok {
		t.Fatal("parseTopCPU found no usage line")
	}
	if usage != 15.5 {
		t.Errorf("usage = %v, want 15.5", usage)
	}
}

func TestReadMemory(t *testing.T) {
	mem := readMemory()
	if mem.TotalBytes == 0 {
		t.Fatal("total memory is zero")
	}
	if mem.UsedBytes > mem.TotalBytes {
		t.Errorf("used %d > total %d", mem.UsedBytes, mem.TotalBytes)
	}
	if mem.UsagePercent < 0 || mem.UsagePercent > 100 {
		t.Errorf("usage = %v", mem.UsagePercent)
	}
}
