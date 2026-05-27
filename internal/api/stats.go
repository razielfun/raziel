//go:build linux

package api

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// handleStats returns system resource usage: CPU, memory, disk, load, sandbox count.
// Called every ~5s by the web dashboard.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	cpu, err := cpuPercent()
	if err != nil {
		jsonInternalError(w, "cpu: "+err.Error())
		return
	}

	memTotal, memUsed, err := memStats()
	if err != nil {
		jsonInternalError(w, "mem: "+err.Error())
		return
	}

	diskTotal, diskUsed, err := diskStats("/")
	if err != nil {
		jsonInternalError(w, "disk: "+err.Error())
		return
	}

	load1, load5, load15, err := loadAvg()
	if err != nil {
		jsonInternalError(w, "load: "+err.Error())
		return
	}

	sandboxes, _ := s.sandboxProvider.List()
	running := 0
	for _, sbx := range sandboxes {
		if sbx.State == "running" {
			running++
		}
	}

	jsonOK(w, map[string]any{
		"cpu_percent":    cpu,
		"mem_total_mb":   memTotal,
		"mem_used_mb":    memUsed,
		"mem_percent":    pct(memUsed, memTotal),
		"disk_total_gb":  diskTotal,
		"disk_used_gb":   diskUsed,
		"disk_percent":   pct(diskUsed, diskTotal),
		"load_1":         load1,
		"load_5":         load5,
		"load_15":        load15,
		"sandbox_count":  len(sandboxes),
		"sandbox_running": running,
		"timestamp":      time.Now().Unix(),
	})
}

func pct(used, total float64) float64 {
	if total == 0 {
		return 0
	}
	return (used / total) * 100
}

// cpuPercent returns CPU utilisation over a 200ms sample window.
func cpuPercent() (float64, error) {
	read := func() (idle, total uint64, err error) {
		f, err := os.Open("/proc/stat")
		if err != nil {
			return 0, 0, err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "cpu ") {
				continue
			}
			fields := strings.Fields(line)[1:]
			var vals [10]uint64
			for i, v := range fields {
				if i >= 10 {
					break
				}
				vals[i], _ = strconv.ParseUint(v, 10, 64)
			}
			// user nice system idle iowait irq softirq steal guest guest_nice
			idle = vals[3] + vals[4]
			for _, v := range vals {
				total += v
			}
			return idle, total, nil
		}
		return 0, 0, fmt.Errorf("cpu line not found")
	}

	idle1, total1, err := read()
	if err != nil {
		return 0, err
	}
	time.Sleep(200 * time.Millisecond)
	idle2, total2, err := read()
	if err != nil {
		return 0, err
	}

	dTotal := float64(total2 - total1)
	dIdle := float64(idle2 - idle1)
	if dTotal == 0 {
		return 0, nil
	}
	return (1 - dIdle/dTotal) * 100, nil
}

func memStats() (totalMB, usedMB float64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		v, _ := strconv.ParseUint(parts[1], 10, 64)
		vals[key] = v
		if len(vals) >= 4 {
			break
		}
	}

	total := float64(vals["MemTotal"]) / 1024
	available := float64(vals["MemAvailable"]) / 1024
	used := total - available
	return total, used, nil
}

func diskStats(path string) (totalGB, usedGB float64, err error) {
	// Use df via /proc/mounts to avoid syscall imports
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	// Fall back to statvfs via a helper — use os.Stat for availability check
	// We'll read /proc/diskstats for a simpler approach:
	// Actually simplest: shell out to statfs via Go's syscall
	return diskStatsSyscall(path)
}

func loadAvg() (load1, load5, load15 float64, err error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected loadavg format")
	}
	load1, _ = strconv.ParseFloat(fields[0], 64)
	load5, _ = strconv.ParseFloat(fields[1], 64)
	load15, _ = strconv.ParseFloat(fields[2], 64)
	return load1, load5, load15, nil
}
