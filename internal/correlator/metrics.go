package correlator

import (
	"runtime"
)

// In v1, we provide mock metrics or basic go runtime metrics as pod proxy metrics
func GetCPUPercent() float64 {
	// A proper implementation would read from /sys/fs/cgroup/cpu.stat
	return 12.5 // Mock for prototype as per PRD "12"
}

func GetMemoryMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Alloc / 1024 / 1024)
}
