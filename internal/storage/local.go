package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
)

// Local 是基于本地文件系统的 Storage 实现。
// 所有 path 必须是绝对路径，否则返回错误。
type Local struct{}

// 编译期断言：Local 必须满足 Storage 接口。
var _ Storage = (*Local)(nil)

// NewLocal 创建一个本地存储实例。无任何配置参数。
func NewLocal() *Local { return &Local{} }

func (l *Local) ensureAbs(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("storage: path must be absolute, got %q", path)
	}
	return nil
}

func (l *Local) Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error {
	if err := l.ensureAbs(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("storage: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", path, err)
	}

	n, err := io.Copy(f, r)
	if err != nil {
		f.Close()
		return fmt.Errorf("storage: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("storage: close %s: %w", path, err)
	}
	if size >= 0 && n != size {
		return fmt.Errorf("storage: write %s: declared size %d but wrote %d bytes", path, size, n)
	}
	return nil
}

func (l *Local) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := l.ensureAbs(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, ErrIsDir
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	return f, nil
}

func (l *Local) Delete(ctx context.Context, path string) error {
	if err := l.ensureAbs(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("storage: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return ErrIsDir
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("storage: remove %s: %w", path, err)
	}
	return nil
}

func (l *Local) DeleteDir(ctx context.Context, path string) error {
	if err := l.ensureAbs(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("storage: remove dir %s: %w", path, err)
	}
	return nil
}

func (l *Local) Stat(ctx context.Context, path string) (*FileInfo, error) {
	if err := l.ensureAbs(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: stat %s: %w", path, err)
	}
	return &FileInfo{
		Path:        path,
		Size:        info.Size(),
		ContentType: detectContentType(path, info.IsDir()),
		ModTime:     info.ModTime(),
		IsDir:       info.IsDir(),
	}, nil
}

func (l *Local) List(ctx context.Context, dir string) ([]*FileInfo, error) {
	if err := l.ensureAbs(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: read dir %s: %w", dir, err)
	}
	out := make([]*FileInfo, 0, len(entries))
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// 并发删除竞态，跳过该项
				continue
			}
			return nil, fmt.Errorf("storage: list dir %s: stat entry %s: %w", dir, e.Name(), err)
		}
		out = append(out, &FileInfo{
			Path:        full,
			Size:        info.Size(),
			ContentType: detectContentType(full, info.IsDir()),
			ModTime:     info.ModTime(),
			IsDir:       info.IsDir(),
		})
	}
	return out, nil
}

// detectContentType 按文件扩展名推断 MIME，目录返回空串。
func detectContentType(path string, isDir bool) string {
	if isDir {
		return ""
	}
	ext := filepath.Ext(path)
	if ext == "" {
		return ""
	}
	return mime.TypeByExtension(ext)
}
