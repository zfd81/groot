package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// fakeUserRepo 仅实现 user reset 需要的 Count/DeleteAll
type fakeUserRepo struct {
	count   int64
	deleted bool
}

func (f *fakeUserRepo) Create(ctx context.Context, u *repo.User) error { return nil }
func (f *fakeUserRepo) GetByUsername(ctx context.Context, username string) (*repo.User, error) {
	return nil, repo.ErrNotFound
}
func (f *fakeUserRepo) GetByID(ctx context.Context, id string) (*repo.User, error) {
	return nil, repo.ErrNotFound
}
func (f *fakeUserRepo) Count(ctx context.Context) (int64, error) { return f.count, nil }
func (f *fakeUserRepo) UpdatePassword(ctx context.Context, id, hash string) error {
	return nil
}
func (f *fakeUserRepo) UpdateLastLogin(ctx context.Context, id string, at time.Time) error {
	return nil
}
func (f *fakeUserRepo) DeleteAll(ctx context.Context) (int64, error) {
	f.deleted = true
	n := f.count
	f.count = 0
	return n, nil
}

func TestParseUserFlags(t *testing.T) {
	flags, err := ParseUserFlags([]string{"reset"})
	if err != nil || flags.Sub != "reset" || flags.Yes {
		t.Errorf("reset: got %+v, %v", flags, err)
	}

	flags, err = ParseUserFlags([]string{"reset", "-y"})
	if err != nil || !flags.Yes {
		t.Errorf("reset -y: got %+v, %v", flags, err)
	}

	if _, err := ParseUserFlags([]string{"unknown"}); err == nil {
		t.Error("unknown subcommand should fail")
	}
	if _, err := ParseUserFlags([]string{"reset", "--bogus"}); err == nil {
		t.Error("unknown flag should fail")
	}
}

// TestRunUserReset_Confirm 输入 y 确认后删除。
func TestRunUserReset_Confirm(t *testing.T) {
	f := &fakeUserRepo{count: 1}
	var out bytes.Buffer
	err := RunUserReset(&UserFlags{Sub: "reset"}, f, strings.NewReader("y\n"), &out)
	if err != nil {
		t.Fatalf("RunUserReset: %v", err)
	}
	if !f.deleted {
		t.Error("confirm with y should delete")
	}
	if !strings.Contains(out.String(), "已删除 1 个用户") {
		t.Errorf("output should report deletion, got %q", out.String())
	}
}

// TestRunUserReset_Cancel 输入 n 取消，不删除。
func TestRunUserReset_Cancel(t *testing.T) {
	f := &fakeUserRepo{count: 1}
	var out bytes.Buffer
	err := RunUserReset(&UserFlags{Sub: "reset"}, f, strings.NewReader("n\n"), &out)
	if err != nil {
		t.Fatalf("RunUserReset: %v", err)
	}
	if f.deleted {
		t.Error("cancel with n should not delete")
	}
	if !strings.Contains(out.String(), "已取消") {
		t.Errorf("output should report cancel, got %q", out.String())
	}
}

// TestRunUserReset_Yes -y 跳过确认直接删除。
func TestRunUserReset_Yes(t *testing.T) {
	f := &fakeUserRepo{count: 2}
	var out bytes.Buffer
	err := RunUserReset(&UserFlags{Sub: "reset", Yes: true}, f, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("RunUserReset: %v", err)
	}
	if !f.deleted {
		t.Error("-y should delete without confirmation")
	}
}

// TestRunUserReset_Empty 表为空时不执行删除。
func TestRunUserReset_Empty(t *testing.T) {
	f := &fakeUserRepo{count: 0}
	var out bytes.Buffer
	err := RunUserReset(&UserFlags{Sub: "reset", Yes: true}, f, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("RunUserReset: %v", err)
	}
	if f.deleted {
		t.Error("empty table should not call DeleteAll")
	}
	if !strings.Contains(out.String(), "无需重置") {
		t.Errorf("output should report empty, got %q", out.String())
	}
}
