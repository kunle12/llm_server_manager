package metrics

import (
	"sync"
	"testing"
)

func TestParseNVIDIA(t *testing.T) {
	out := `NVIDIA GeForce RTX 4090, 42, 8192, 24576
NVIDIA GeForce RTX 4090, 70, 4096, 24576
`
	gpu, ok := parseNVIDIA(out)
	if !ok {
		t.Fatal("parseNVIDIA returned no GPU")
	}
	if gpu.Name != "NVIDIA GeForce RTX 4090" || gpu.Count != 2 {
		t.Errorf("name/count = %q/%d", gpu.Name, gpu.Count)
	}
	if gpu.UsagePercent != 70 {
		t.Errorf("usage = %v, want 70", gpu.UsagePercent)
	}
	wantMiB := uint64((8192 + 4096) * 1024 * 1024)
	if gpu.MemoryUsedBytes != wantMiB {
		t.Errorf("used = %d, want %d", gpu.MemoryUsedBytes, wantMiB)
	}
	if gpu.MemoryTotalBytes != uint64(2*24576*1024*1024) {
		t.Errorf("total = %d", gpu.MemoryTotalBytes)
	}
	if gpu.Source != "nvidia-smi" {
		t.Errorf("source = %q", gpu.Source)
	}
}

func TestParseNVIDIADiscardsUnavailableRows(t *testing.T) {
	out := "GPU-0, [N/A], [N/A], [N/A]\n"
	if _, ok := parseNVIDIA(out); ok {
		t.Fatal("expected no GPU for unavailable rows")
	}
}

func TestParseIOReg(t *testing.T) {
	out := `"model" = "Apple M4"
"gpu-core-count" = 10
"PerformanceStatistics" = {"In use system memory (driver)"=0,"Tiler Utilization %"=7,"Device Utilization %"=14,"In use system memory"=383369216}`
	gpu, ok := parseIOReg(out)
	if !ok {
		t.Fatal("parseIOReg returned no GPU")
	}
	if gpu.Name != "Apple M4" || gpu.CoreCount != 10 {
		t.Errorf("name/cores = %q/%d", gpu.Name, gpu.CoreCount)
	}
	if gpu.UsagePercent != 14 {
		t.Errorf("usage = %v, want 14", gpu.UsagePercent)
	}
	// The driver-specific allocation must not be counted as in-use memory.
	if gpu.MemoryUsedBytes != 383369216 {
		t.Errorf("used memory = %d", gpu.MemoryUsedBytes)
	}
}

func TestParseROCm(t *testing.T) {
	out := `{
	  "card0": {
	    "GPU use (%)": "37",
	    "VRAM Total Used Memory (B)": "1073741824",
	    "VRAM Total Memory (B)": "17179869184",
	    "Card series": "Radeon RX 7900 XTX"
	  },
	  "system": {"Driver version": "6.8"}
	}`
	gpu, ok := parseROCm([]byte(out))
	if !ok {
		t.Fatal("parseROCm returned no GPU")
	}
	if gpu.Name != "Radeon RX 7900 XTX" || gpu.Count != 1 {
		t.Errorf("name/count = %q/%d", gpu.Name, gpu.Count)
	}
	if gpu.UsagePercent != 37 {
		t.Errorf("usage = %v, want 37", gpu.UsagePercent)
	}
	if gpu.MemoryUsedBytes != 1073741824 || gpu.MemoryTotalBytes != 17179869184 {
		t.Errorf("memory = %d/%d", gpu.MemoryUsedBytes, gpu.MemoryTotalBytes)
	}
}

func TestCollectorSnapshotHasCPUAndMemory(t *testing.T) {
	c := NewCollector()
	snap := c.Snapshot()
	if snap.CPU.Cores <= 0 {
		t.Errorf("cores = %d, want > 0", snap.CPU.Cores)
	}
	if snap.Memory.TotalBytes == 0 {
		t.Error("memory total is zero")
	}
	if snap.Memory.UsagePercent < 0 || snap.Memory.UsagePercent > 100 {
		t.Errorf("memory usage = %v, want 0..100", snap.Memory.UsagePercent)
	}
	if snap.Timestamp.IsZero() {
		t.Error("timestamp is zero")
	}
}

func TestCollectorConcurrentSnapshots(t *testing.T) {
	c := NewCollector()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				snap := c.Snapshot()
				if snap.CPU.Cores <= 0 {
					t.Errorf("cores = %d", snap.CPU.Cores)
					return
				}
			}
		}()
	}
	wg.Wait()
}
