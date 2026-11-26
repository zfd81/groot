//go:build !windows

package pysvc

import (
	"os"
	"syscall"
)

// terminate 向子进程发送 SIGTERM，触发其优雅停止。
func terminate(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
