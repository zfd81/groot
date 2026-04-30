package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cloudwego/eino/adk/filesystem"
)

// SymlinkBackend wraps a filesystem.Backend and overrides GlobInfo to follow
// directory symlinks. All other methods delegate to the inner backend.
type SymlinkBackend struct {
	inner filesystem.Backend
}

// NewSymlinkBackend creates a symlink-aware backend wrapper.
func NewSymlinkBackend(inner filesystem.Backend) *SymlinkBackend {
	return &SymlinkBackend{inner: inner}
}

// GlobInfo walks the directory tree following symlinks to directories,
// then delegates pattern matching to doublestar (same as eino local backend).
func (b *SymlinkBackend) GlobInfo(ctx context.Context, req *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	path := filepath.Clean(req.Path)

	var matches []string
	err := walkWithSymlinks(path, func(relPath string) error {
		relPath = filepath.ToSlash(relPath)
		matched, _ := doublestar.Match(req.Pattern, relPath)
		if matched {
			matches = append(matches, relPath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(matches)

	files := make([]filesystem.FileInfo, 0, len(matches))
	for _, match := range matches {
		files = append(files, filesystem.FileInfo{Path: match})
	}
	return files, nil
}

// walkWithSymlinks is like filepath.WalkDir but follows directory symlinks.
func walkWithSymlinks(root string, fn func(relPath string) error) error {
	return walkDir(root, root, fn)
}

func walkDir(root string, dir string, fn func(relPath string) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsPermission(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		relPath, _ := filepath.Rel(root, fullPath)

		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			// Resolve symlink to check if it points to a directory
			if target, statErr := os.Stat(fullPath); statErr == nil {
				isDir = target.IsDir()
			}
		}

		if err := fn(relPath); err != nil {
			return err
		}

		if isDir {
			if err := walkDir(root, fullPath, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

// LsInfo delegates to the inner backend.
func (b *SymlinkBackend) LsInfo(ctx context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	return b.inner.LsInfo(ctx, req)
}

// Read delegates to the inner backend.
func (b *SymlinkBackend) Read(ctx context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	return b.inner.Read(ctx, req)
}

// GrepRaw delegates to the inner backend.
func (b *SymlinkBackend) GrepRaw(ctx context.Context, req *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	return b.inner.GrepRaw(ctx, req)
}

// Write delegates to the inner backend.
func (b *SymlinkBackend) Write(ctx context.Context, req *filesystem.WriteRequest) error {
	return b.inner.Write(ctx, req)
}

// Edit delegates to the inner backend.
func (b *SymlinkBackend) Edit(ctx context.Context, req *filesystem.EditRequest) error {
	return b.inner.Edit(ctx, req)
}
