//go:build !linux && !darwin

package metrics

// Other platforms are not supported by the manager's process layer; keep the
// package buildable and report an empty (rather than failing) snapshot.
type cpuSampler struct{}

func newCPUSampler() cpuSampler      { return cpuSampler{} }
func (s *cpuSampler) Usage() float64 { return 0 }
func readMemory() MemoryStats        { return MemoryStats{} }
func readLoad() []float64            { return nil }
func readCPUModel() string           { return "" }
