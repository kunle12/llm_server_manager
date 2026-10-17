//go:build linux

package metrics

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cpuSampler derives usage from the cumulative counters in /proc/stat. It
// keeps the previous sample, so each call reports the rate since the last one.
type cpuSampler struct {
	mu        sync.Mutex
	prevTotal uint64
	prevIdle  uint64
	primed    bool
	lastAt    time.Time
	lastUsage float64
}

func newCPUSampler() cpuSampler {
	s := cpuSampler{}
	_ = s.Usage() // prime the counters for the first real request
	return s
}

func (s *cpuSampler) Usage() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	total, idle, ok := parseProcStat(string(data))
	if !ok {
		return 0
	}

	now := time.Now()
	if !s.primed || total < s.prevTotal || idle < s.prevIdle {
		s.prevTotal, s.prevIdle, s.primed, s.lastAt = total, idle, true, now
		return 0
	}

	// The counters are cumulative. If nobody has sampled for a long time
	// (e.g. the UI was closed), a delta from the old baseline would describe
	// the whole idle period, not current load. Take a short fresh sample.
	if now.Sub(s.lastAt) > 2*time.Minute {
		return s.freshSample(total, idle)
	}

	deltaTotal := total - s.prevTotal
	deltaIdle := idle - s.prevIdle
	s.prevTotal, s.prevIdle, s.lastAt = total, idle, now
	if deltaTotal == 0 {
		return s.lastUsage
	}
	usage := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
	s.lastUsage = usage
	return usage
}

// freshSample resets the baseline and measures a 100ms window, so the first
// request after a long gap still reports current CPU load. The caller holds
// the sampler lock; other concurrent requests wait for this short sample.
func (s *cpuSampler) freshSample(prevTotal, prevIdle uint64) float64 {
	s.prevTotal, s.prevIdle = prevTotal, prevIdle
	time.Sleep(100 * time.Millisecond)

	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		s.lastAt = time.Now()
		return 0
	}
	total, idle, ok := parseProcStat(string(data))
	if !ok || total < prevTotal || idle < prevIdle {
		s.lastAt = time.Now()
		return 0
	}

	deltaTotal := total - prevTotal
	deltaIdle := idle - prevIdle
	s.prevTotal, s.prevIdle, s.lastAt = total, idle, time.Now()
	if deltaTotal == 0 {
		return s.lastUsage
	}
	usage := float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
	s.lastUsage = usage
	return usage
}

// parseProcStat reads the aggregate "cpu" line. Fields are user, nice, system,
// idle, iowait, irq, softirq, steal (guest time is already included in user).
func parseProcStat(data string) (total, idle uint64, ok bool) {
	for _, line := range strings.Split(data, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		for i, field := range fields {
			// Guest time (fields 8 and 9) is already included in user/nice;
			// counting it again would inflate the total.
			if i >= 8 {
				continue
			}
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				continue
			}
			total += value
			if i == 3 || i == 4 { // idle + iowait
				idle += value
			}
		}
		return total, idle, len(fields) > 0
	}
	return 0, 0, false
}

func readMemory() MemoryStats {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemoryStats{}
	}

	values := make(map[string]uint64)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
			values[key] = value
		}
	}

	total := values["MemTotal"] * 1024
	available := values["MemAvailable"] * 1024
	if available == 0 {
		available = (values["MemFree"] + values["Buffers"] + values["Cached"]) * 1024
	}
	if total == 0 {
		return MemoryStats{}
	}
	if available > total {
		available = total
	}
	used := total - available
	return MemoryStats{
		UsedBytes:    used,
		TotalBytes:   total,
		UsagePercent: float64(used) / float64(total) * 100,
	}
}

func readLoad() []float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(data))
	out := make([]float64, 0, 3)
	for i := 0; i < 3 && i < len(fields); i++ {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil
		}
		out = append(out, round1(value))
	}
	return out
}

func readCPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
