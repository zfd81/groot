package resourcelocal

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zfd81/groot/internal/repo"
)

type localRepo struct {
	homeDir string
}

func New(homeDir string) repo.ResourceRepo {
	return &localRepo{homeDir: homeDir}
}

func (r *localRepo) abs(path string) string {
	return filepath.Join(r.homeDir, filepath.FromSlash(path))
}

func sha1Hex(content []byte) string {
	h := sha1.Sum(content)
	return fmt.Sprintf("%x", h)
}

func (r *localRepo) Put(ctx context.Context, res *repo.Resource) error {
	absPath := r.abs(res.Path)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return err
	}
	tmp := absPath + ".tmp"
	if err := os.WriteFile(tmp, res.Content, 0644); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, absPath)
}

func (r *localRepo) Get(ctx context.Context, path string) (*repo.Resource, error) {
	absPath := r.abs(path)
	content, err := os.ReadFile(absPath)
	if os.IsNotExist(err) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	info, _ := os.Stat(absPath)
	return &repo.Resource{
		Path: path, Content: content,
		Size:        int64(len(content)),
		ContentHash: sha1Hex(content),
		UpdatedAt:   info.ModTime(),
	}, nil
}

func (r *localRepo) Stat(ctx context.Context, path string) (*repo.ResourceEntry, error) {
	absPath := r.abs(path)
	content, err := os.ReadFile(absPath)
	if os.IsNotExist(err) {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	info, _ := os.Stat(absPath)
	return &repo.ResourceEntry{
		Path:        path,
		Size:        int64(len(content)),
		ContentHash: sha1Hex(content),
		UpdatedAt:   info.ModTime(),
	}, nil
}

func (r *localRepo) List(ctx context.Context, prefix string) ([]*repo.ResourceEntry, error) {
	base := r.homeDir
	if prefix != "" {
		base = filepath.Join(r.homeDir, filepath.FromSlash(prefix))
	}
	var entries []*repo.ResourceEntry
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(r.homeDir, path)
		entries = append(entries, &repo.ResourceEntry{
			Path:        filepath.ToSlash(rel),
			Size:        int64(len(content)),
			ContentHash: sha1Hex(content),
			UpdatedAt:   info.ModTime(),
		})
		return nil
	})
	return entries, err
}

func (r *localRepo) Delete(ctx context.Context, path string) error {
	err := os.Remove(r.abs(path))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
