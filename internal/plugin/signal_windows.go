//go:build windows

package plugin

import "os"

// terminate 在 Windows 上不发送信号：优雅停止依赖关闭 stdin 生命线。
func terminate(proc *os.Process) error {
	return nil
}
