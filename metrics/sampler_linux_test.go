//go:build linux

package metrics

import "testing"

func TestParseProcStat(t *testing.T) {
	data := `cpu  100 20 30 400 50 5 5 0 7 9
cpu0 50 10 15 200 25 2 2 0 0 0
`
	total, idle, ok := parseProcStat(data)
	if !ok {
		t.Fatal("parseProcStat returned no aggregate line")
	}
	if total != 610 {
		t.Errorf("total = %d, want 610", total)
	}
	if idle != 450 { // idle + iowait
		t.Errorf("idle = %d, want 450", idle)
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
}
