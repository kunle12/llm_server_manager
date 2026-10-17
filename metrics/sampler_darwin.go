//go:build darwin

package metrics

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cpuSampler shells out to top: macOS does not expose per-tick CPU counters
// through a plain syscall, and top's first (`-l 1`) sample is already an
// interval sample. The result is cached briefly so a burst of requests pays
// the ~0.3s cost only once.
type cpuSampler struct {
	mu   sync.Mutex
	last float64
	at   time.Time
}

func newCPUSampler() cpuSampler { return cpuSampler{} }

func (s *cpuSampler) Usage() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.at.IsZero() && time.Since(s.at) < 2*time.Second {
		return s.last
	}

	out, err := runOutputTimeout(3*time.Second, "/usr/bin/top", "-l", "1", "-n", "0", "-stats", "pid,cpu")
	if err != nil {
		return s.last
	}
	usage, ok := parseTopCPU(out)
	if !ok {
		return s.last
	}
	s.last = usage
	s.at = time.Now()
	return usage
}

func parseTopCPU(out string) (float64, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		for _, part := range strings.Split(strings.TrimPrefix(line, "CPU usage:"), ",") {
			part = strings.TrimSpace(part)
			if !strings.HasSuffix(part, "idle") {
				continue
			}
			value := strings.TrimSpace(strings.TrimSuffix(part, "idle"))
			value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
			if idle, err := strconv.ParseFloat(value, 64); err == nil {
				return clampPercent(100 - idle), true
			}
		}
	}
	return 0, false
}

var vmPageSize = regexp.MustCompile(`page size of (\d+) bytes`)

// readMemory follows Activity Monitor: memory used is active + wired +
// compressor pages. Free/inactive/speculative pages are treated as available.
func readMemory() MemoryStats {
	totalOut, err := runOutputTimeout(2*time.Second, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if err != nil {
		return MemoryStats{}
	}
	total, err := strconv.ParseUint(strings.TrimSpace(totalOut), 10, 64)
	if err != nil || total == 0 {
		return MemoryStats{}
	}

	vmOut, err := runOutputTimeout(2*time.Second, "/usr/bin/vm_stat")
	if err != nil {
		return MemoryStats{TotalBytes: total}
	}

	pageSize := uint64(4096)
	if match := vmPageSize.FindStringSubmatch(vmOut); match != nil {
		if size, err := strconv.ParseUint(match[1], 10, 64); err == nil {
			pageSize = size
		}
	}

	pages := make(map[string]uint64)
	for _, line := range strings.Split(vmOut, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSuffix(strings.TrimSpace(value), ".")
		if count, err := strconv.ParseUint(value, 10, 64); err == nil {
			pages[strings.TrimSpace(key)] = count
		}
	}

	used := (pages["Pages active"] + pages["Pages wired down"] + pages["Pages occupied by compressor"]) * pageSize
	if used > total {
		used = total
	}
	return MemoryStats{
		UsedBytes:    used,
		TotalBytes:   total,
		UsagePercent: float64(used) / float64(total) * 100,
	}
}

func readLoad() []float64 {
	out, err := runOutputTimeout(time.Second, "/usr/sbin/sysctl", "-n", "vm.loadavg")
	if err != nil {
		return nil
	}
	fields := strings.Fields(strings.Trim(strings.TrimSpace(out), "{}"))
	result := make([]float64, 0, 3)
	for i := 0; i < 3 && i < len(fields); i++ {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil
		}
		result = append(result, round1(value))
	}
	return result
}

func readCPUModel() string {
	out, err := runOutputTimeout(time.Second, "/usr/sbin/sysctl", "-n", "machdep.cpu.brand_string")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
