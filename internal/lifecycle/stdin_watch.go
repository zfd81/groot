package lifecycle

import "io"

// WatchStdin 在后台持续读取 r 并丢弃内容，直到遇到 EOF 或读错误，然后调用 onEOF 一次。
// 工作进程用它监听监督进程关闭管道的"请停下来"通知。
// 只在受监督模式下使用：单进程模式的 stdin 可能是终端（永不 EOF）或 /dev/null（立即 EOF）。
func WatchStdin(r io.Reader, onEOF func()) {
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := r.Read(buf); err != nil {
				onEOF()
				return
			}
		}
	}()
}
