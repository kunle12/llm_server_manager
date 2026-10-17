// Package metrics collects host resource telemetry (CPU, memory, GPU) for the
// web UI. Collection is best-effort: CPU and memory are read from the OS's
// native interfaces, while GPU data depends on an available vendor tool
// (nvidia-smi, rocm-smi) or, on macOS, the IORegistry. When no GPU source can
// be read the GPU block reports available=false so callers always get a usable
// snapshot.
package metrics

import (
	"context"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Snapshot is one point-in-time view of the host, served by GET
// /api/v1/system.
type Snapshot struct {
	CPU       CPUStats    `json:"cpu"`
	Memory    MemoryStats `json:"memory"`
	GPU       GPUStats    `json:"gpu"`
	Load      []float64   `json:"load,omitempty"`
	Hostname  string      `json:"hostname,omitempty"`
	Platform  string      `json:"platform,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

type CPUStats struct {
	UsagePercent float64 `json:"usage_percent"`
	Cores        int     `json:"cores"`
	Model        string  `json:"model,omitempty"`
}

type MemoryStats struct {
	UsedBytes    uint64  `json:"used_bytes"`
	TotalBytes   uint64  `json:"total_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

type GPUStats struct {
	Available        bool    `json:"available"`
	UsagePercent     float64 `json:"usage_percent"`
	MemoryUsedBytes  uint64  `json:"memory_used_bytes"`
	MemoryTotalBytes uint64  `json:"memory_total_bytes"`
	Name             string  `json:"name,omitempty"`
	Count            int     `json:"count,omitempty"`
	CoreCount        int     `json:"core_count,omitempty"`
	Source           string  `json:"source,omitempty"`
}

// Provider is the narrow surface the HTTP handler depends on. *Collector
// implements it; tests can substitute a deterministic provider.
type Provider interface {
	Snapshot() Snapshot
}

// Collector samples the host. It is safe for concurrent use.
type Collector struct {
	cpu      cpuSampler
	cores    int
	cpuModel string
	hostname string

	gpuMu sync.Mutex
	gpu   GPUStats
	gpuAt time.Time
}

// gpuCacheTTL avoids spawning a vendor tool for every request. The UI polls
// every few seconds; a short cache keeps rapid manual refreshes cheap.
const gpuCacheTTL = 2 * time.Second

func NewCollector() *Collector {
	c := &Collector{
		cpu:   newCPUSampler(),
		cores: runtime.NumCPU(),
	}
	c.cpuModel = readCPUModel()
	if hostname, err := os.Hostname(); err == nil {
		c.hostname = hostname
	}
	return c
}

// Snapshot collects the current host state.
func (c *Collector) Snapshot() Snapshot {
	// CPU, memory, load and GPU are independent and each may spawn a
	// short-lived OS command on macOS. Run them concurrently so a slow GPU
	// probe or sysctl cannot stack up behind the others.
	var (
		memory MemoryStats
		load   []float64
		gpu    GPUStats
		wg     sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		memory = readMemory()
	}()
	go func() {
		defer wg.Done()
		load = readLoad()
	}()
	go func() {
		defer wg.Done()
		gpu = c.gpuSnapshot()
	}()
	cpuUsage := c.cpu.Usage()
	wg.Wait()

	memory.UsagePercent = round1(clampPercent(memory.UsagePercent))

	out := Snapshot{
		CPU: CPUStats{
			UsagePercent: round1(clampPercent(cpuUsage)),
			Cores:        c.cores,
			Model:        c.cpuModel,
		},
		Memory:    memory,
		Hostname:  c.hostname,
		Platform:  runtime.GOOS,
		Timestamp: time.Now(),
	}

	if len(load) == 3 {
		out.Load = load
	}
	out.GPU = gpu
	return out
}

func (c *Collector) gpuSnapshot() GPUStats {
	c.gpuMu.Lock()
	defer c.gpuMu.Unlock()

	if !c.gpuAt.IsZero() && time.Since(c.gpuAt) < gpuCacheTTL {
		return c.gpu
	}

	c.gpu = collectGPU()
	c.gpuAt = time.Now()
	return c.gpu
}

// runOutputTimeout runs a fixed-argument command and returns stdout. A context
// timeout guarantees a hung vendor tool cannot block the HTTP handler.
func runOutputTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func clampPercent(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
