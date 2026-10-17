package metrics

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const gpuCommandTimeout = 1500 * time.Millisecond

// collectGPU tries each vendor source in turn. Tools are invoked with fixed
// arguments only, so a shell is never involved.
func collectGPU() GPUStats {
	if gpu, ok := gpuFromNVIDIA(); ok {
		return gpu
	}
	if gpu, ok := gpuFromROCm(); ok {
		return gpu
	}
	if runtime.GOOS == "darwin" {
		if gpu, ok := gpuFromIOReg(); ok {
			return gpu
		}
	}
	return GPUStats{Available: false}
}

func runOutput(name string, args ...string) (string, error) {
	return runOutputTimeout(gpuCommandTimeout, name, args...)
}

// ---------------------------------------------------------------- NVIDIA

// nvidiaSMI is installed with the NVIDIA driver and normally lives on PATH.
// A daemon/service may start with a restricted PATH, so fall back to the
// standard driver locations used on Linux and in WSL containers.
var nvidiaSMIPaths = []string{
	"nvidia-smi",
	"/usr/bin/nvidia-smi",
	"/usr/local/bin/nvidia-smi",
	"/usr/local/nvidia/bin/nvidia-smi",
	"/usr/lib/wsl/lib/nvidia-smi",
}

func findNvidiaSMI() (string, bool) {
	for _, candidate := range nvidiaSMIPaths {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, true
		}
	}
	return "", false
}

func gpuFromNVIDIA() (GPUStats, bool) {
	bin, ok := findNvidiaSMI()
	if !ok {
		return GPUStats{}, false
	}
	out, err := runOutput(
		bin,
		"--query-gpu=name,utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits",
	)
	if err != nil {
		return GPUStats{}, false
	}
	return parseNVIDIA(out)
}

// parseNVIDIA reads `name, utilization.gpu, memory.used, memory.total` rows
// (MiB for memory). Some devices expose a row but report N/A for unified
// memory (for example NVIDIA GB10); those rows are still a valid GPU, so
// memory fields are optional as long as utilization is readable.
func parseNVIDIA(out string) (GPUStats, bool) {
	gpu := GPUStats{Available: true, Source: "nvidia-smi"}
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}

		usageOK := false
		usage := 0.0
		if value, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64); err == nil {
			usage = value
			usageOK = true
		}

		used, usedOK := uint64(0), false
		if len(fields) > 2 {
			used, usedOK = parseMiB(fields[2])
		}
		total, totalOK := uint64(0), false
		if len(fields) > 3 {
			total, totalOK = parseMiB(fields[3])
		}
		// A row with no readable telemetry at all is not useful.
		if !usageOK && !usedOK && !totalOK {
			continue
		}

		if rows == 0 {
			gpu.Name = name
		}
		rows++
		if usageOK {
			if u := clampPercent(usage); u > gpu.UsagePercent {
				gpu.UsagePercent = round1(u)
			}
		}
		if usedOK {
			gpu.MemoryUsedBytes += used
		}
		if totalOK {
			gpu.MemoryTotalBytes += total
		}
	}
	if rows == 0 {
		return GPUStats{}, false
	}
	gpu.Count = rows
	return gpu, true
}

func parseMiB(s string) (uint64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return uint64(v * 1024 * 1024), true
}

// ---------------------------------------------------------------- ROCm

func gpuFromROCm() (GPUStats, bool) {
	out, err := runOutput("rocm-smi", "--showuse", "--showmeminfo", "vram", "--json")
	if err != nil {
		return GPUStats{}, false
	}
	return parseROCm([]byte(out))
}

// parseROCm handles the loosely-typed JSON emitted by rocm-smi: keys and
// value types vary between releases, so values are located by name.
func parseROCm(data []byte) (GPUStats, bool) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return GPUStats{}, false
	}

	gpu := GPUStats{Available: true, Source: "rocm-smi"}
	rows := 0
	for _, value := range raw {
		card, ok := value.(map[string]any)
		if !ok {
			continue
		}

		usage, hasUsage := findNumber(card, func(k string) bool {
			return strings.Contains(k, "gpu use") || strings.Contains(k, "gpu utilization")
		})
		used, hasUsed := findNumber(card, func(k string) bool {
			return strings.Contains(k, "vram total used memory")
		})
		total, hasTotal := findNumber(card, func(k string) bool {
			return strings.Contains(k, "vram total memory")
		})
		if !hasUsage && !hasUsed && !hasTotal {
			continue
		}

		rows++
		if rows == 1 {
			gpu.Name = findString(card, func(k string) bool {
				return strings.Contains(k, "card series") || strings.Contains(k, "card model") || k == "gpu id"
			})
		}
		if hasUsage {
			if u := clampPercent(usage); u > gpu.UsagePercent {
				gpu.UsagePercent = round1(u)
			}
		}
		if hasUsed {
			gpu.MemoryUsedBytes += uint64(maxFloat(used, 0))
		}
		if hasTotal {
			gpu.MemoryTotalBytes += uint64(maxFloat(total, 0))
		}
	}
	if rows == 0 {
		return GPUStats{}, false
	}
	gpu.Count = rows
	return gpu, true
}

func findNumber(m map[string]any, match func(string) bool) (float64, bool) {
	for key, value := range m {
		if !match(strings.ToLower(key)) {
			continue
		}
		switch v := value.(type) {
		case float64:
			return v, true
		case json.Number:
			f, err := v.Float64()
			return f, err == nil
		case string:
			s := strings.TrimSuffix(strings.TrimSpace(v), "%")
			f, err := strconv.ParseFloat(s, 64)
			return f, err == nil
		}
	}
	return 0, false
}

func findString(m map[string]any, match func(string) bool) string {
	for key, value := range m {
		if !match(strings.ToLower(key)) {
			continue
		}
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func maxFloat(v, floor float64) float64 {
	if v < floor {
		return floor
	}
	return v
}

// ---------------------------------------------------------------- Apple

var (
	ioregDeviceUse = regexp.MustCompile(`"Device Utilization %"\s*=\s*(\d+)`)
	ioregTilerUse  = regexp.MustCompile(`"Tiler Utilization %"\s*=\s*(\d+)`)
	ioregMemInUse  = regexp.MustCompile(`"In use system memory"\s*=\s*(\d+)`)
	ioregModel     = regexp.MustCompile(`"model"\s*=\s*"([^"]+)"`)
	ioregGPUCount  = regexp.MustCompile(`"gpu-core-count"\s*=\s*(\d+)`)
)

func gpuFromIOReg() (GPUStats, bool) {
	// ioreg lives in /usr/sbin, which may be absent from PATH for a process
	// launched by a service manager. Use the fixed system path.
	out, err := runOutput("/usr/sbin/ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator")
	if err != nil {
		return GPUStats{}, false
	}
	return parseIOReg(out)
}

// parseIOReg reads the IOAccelerator performance statistics that macOS exposes
// without elevated privileges. Apple Silicon has no dedicated VRAM, so only
// the in-use system memory is reported.
func parseIOReg(out string) (GPUStats, bool) {
	uses := ioregDeviceUse.FindAllStringSubmatch(out, -1)
	if len(uses) == 0 {
		uses = ioregTilerUse.FindAllStringSubmatch(out, -1)
	}
	memories := ioregMemInUse.FindAllStringSubmatch(out, -1)
	if len(uses) == 0 && len(memories) == 0 {
		return GPUStats{}, false
	}

	gpu := GPUStats{Available: true, Source: "ioreg"}
	for _, match := range uses {
		if v, err := strconv.ParseFloat(match[1], 64); err == nil {
			if u := clampPercent(v); u > gpu.UsagePercent {
				gpu.UsagePercent = round1(u)
			}
		}
	}
	for _, match := range memories {
		if v, err := strconv.ParseUint(match[1], 10, 64); err == nil {
			gpu.MemoryUsedBytes += v
		}
	}
	if match := ioregModel.FindStringSubmatch(out); match != nil {
		gpu.Name = match[1]
	}
	if match := ioregGPUCount.FindStringSubmatch(out); match != nil {
		if cores, err := strconv.Atoi(match[1]); err == nil {
			gpu.CoreCount = cores
		}
	}
	gpu.Count = max(len(uses), 1)
	return gpu, true
}
