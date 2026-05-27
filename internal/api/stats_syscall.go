//go:build linux

package api

import (
	"fmt"
	"syscall"
)

func diskStatsSyscall(path string) (totalGB, usedGB float64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	total := float64(stat.Blocks) * float64(stat.Bsize)
	free := float64(stat.Bfree) * float64(stat.Bsize)
	used := total - free
	return total / (1024 * 1024 * 1024), used / (1024 * 1024 * 1024), nil
}
