package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocal_RejectsRelativePath(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	err := ls.Write(ctx, "relative/path.txt", strings.NewReader("x"), 1, "text/plain")
	if err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path error, got: %v", err)
	}
}

func TestLocal_WriteReadDeleteCycle(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()
	path := filepath.Join(dir, "sub", "hello.txt")
	want := []byte("hello world")

	if err := ls.Write(ctx, path, bytes.NewReader(want), int64(len(want)), "text/plain"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := ls.Read(ctx, path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %q, want %q", got, want)
	}

	if err := ls.Delete(ctx, path); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should not exist after Delete, stat err: %v", err)
	}
}

func TestLocal_StatNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, err := ls.Stat(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocal_ReadNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, err := ls.Read(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocal_ReadReturnsErrIsDir(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()
	_, err := ls.Read(ctx, dir)
	if !errors.Is(err, ErrIsDir) {
		t.Fatalf("expected ErrIsDir, got: %v", err)
	}
}

func TestLocal_DeleteNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	err := ls.Delete(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocal_DeleteReturnsErrIsDir(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	err := ls.Delete(ctx, subdir)
	if !errors.Is(err, ErrIsDir) {
		t.Fatalf("expected ErrIsDir, got: %v", err)
	}
}

func TestLocal_DeleteDirRecursive(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()

	subFile := filepath.Join(dir, "sub", "a.txt")
	if err := ls.Write(ctx, subFile, strings.NewReader("a"), 1, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := ls.DeleteDir(ctx, filepath.Join(dir, "sub")); err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Fatalf("sub dir should not exist, stat err: %v", err)
	}
}

func TestLocal_DeleteDirMissingIsNoop(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "ghost")
	if err := ls.DeleteDir(ctx, missing); err != nil {
		t.Fatalf("DeleteDir on missing dir should be no-op, got: %v", err)
	}
}

func TestLocal_ListReturnsFiles(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()

	for _, name := range []string{"a.txt", "b.json"} {
		if err := ls.Write(ctx, filepath.Join(dir, name), strings.NewReader("x"), 1, ""); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	infos, err := ls.List(ctx, dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(infos))
	}
	var sawChild bool
	for _, fi := range infos {
		if fi.IsDir && filepath.Base(fi.Path) == "child" {
			sawChild = true
		}
	}
	if !sawChild {
		t.Fatal("expected child dir entry with IsDir=true")
	}
}

func TestLocal_ListNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "ghost")
	_, err := ls.List(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocal_WriteRejectsSizeMismatch(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()
	path := filepath.Join(dir, "x.txt")
	// 声明 100 字节，实际只有 5 字节
	err := ls.Write(ctx, path, strings.NewReader("hello"), 100, "")
	if err == nil {
		t.Fatal("expected size mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "declared size") {
		t.Fatalf("expected size mismatch error, got: %v", err)
	}
}

func TestLocal_WriteAcceptsNegativeSize(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()
	path := filepath.Join(dir, "x.txt")
	// size < 0 表示长度未知，不应校验
	if err := ls.Write(ctx, path, strings.NewReader("hello"), -1, ""); err != nil {
		t.Fatalf("Write with size=-1 should succeed, got: %v", err)
	}
}

func TestLocal_RenameFileMovesIt(t *testing.T) {
	s := NewLocal()
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	if err := s.Write(ctx, src, strings.NewReader("hello"), 5, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Rename(ctx, src, dst); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	// src 不存在了
	if _, err := s.Stat(ctx, src); !errors.Is(err, ErrNotFound) {
		t.Errorf("src should be gone, got err=%v", err)
	}
	// dst 存在且内容正确
	rc, err := s.Read(ctx, dst)
	if err != nil {
		t.Fatalf("Read dst: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "hello" {
		t.Errorf("dst content = %q, want hello", body)
	}
}

func TestLocal_RenameSrcNotFound(t *testing.T) {
	s := NewLocal()
	dir := t.TempDir()
	err := s.Rename(context.Background(), filepath.Join(dir, "nope"), filepath.Join(dir, "dst"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLocal_RenameRejectsRelativePath(t *testing.T) {
	s := NewLocal()
	if err := s.Rename(context.Background(), "rel/src", "rel/dst"); err == nil {
		t.Error("expected error for relative paths")
	}
}

func TestLocal_RenameOverwritesDst(t *testing.T) {
	s := NewLocal()
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	dst := filepath.Join(dir, "b.txt")
	_ = s.Write(ctx, src, strings.NewReader("new"), 3, "")
	_ = s.Write(ctx, dst, strings.NewReader("old"), 3, "")
	if err := s.Rename(ctx, src, dst); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	rc, _ := s.Read(ctx, dst)
	body, _ := io.ReadAll(rc)
	rc.Close()
	if string(body) != "new" {
		t.Errorf("dst should be overwritten, got %q", body)
	}
}
