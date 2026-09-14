package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/repo"
	grootsync "github.com/zfd81/groot/internal/sync"
)

// TestSyncErrorStatus_DisabledMapsTo409 锁定禁用错误映射 409：
// 前端依赖 409 sync_disabled 隐藏同步入口。
func TestSyncErrorStatus_DisabledMapsTo409(t *testing.T) {
	code, status := syncErrorStatus(grootsync.ErrSyncDisabled)
	if code != 409 {
		t.Fatalf("code = %d, want 409", code)
	}
	if status != "sync_disabled" {
		t.Fatalf("status = %q, want sync_disabled", status)
	}
}

// TestSyncErrorStatus_WrappedDisabledMapsTo409 锁定 wrapped 情形：
// 判断必须用 errors.Is 而非 ==，包裹后的 ErrSyncDisabled 同样映射 409。
func TestSyncErrorStatus_WrappedDisabledMapsTo409(t *testing.T) {
	err := fmt.Errorf("manager init: %w", grootsync.ErrSyncDisabled)
	code, status := syncErrorStatus(err)
	if code != 409 {
		t.Fatalf("code = %d, want 409", code)
	}
	if status != "sync_disabled" {
		t.Fatalf("status = %q, want sync_disabled", status)
	}
}

// TestSyncErrorStatus_ValidationMapsTo400 验证路径校验错误映射 400。
func TestSyncErrorStatus_ValidationMapsTo400(t *testing.T) {
	err := grootsync.ValidateSyncPath("../../etc/passwd")
	if err == nil {
		t.Fatal("ValidateSyncPath should reject traversal path")
	}
	code, status := syncErrorStatus(err)
	if code != 400 {
		t.Fatalf("code = %d, want 400", code)
	}
	if status != "invalid_request" {
		t.Fatalf("status = %q, want invalid_request", status)
	}
}

// TestSyncErrorStatus_OtherMapsTo500 验证其他错误映射 500。
func TestSyncErrorStatus_OtherMapsTo500(t *testing.T) {
	code, status := syncErrorStatus(errors.New("db connection refused"))
	if code != 500 {
		t.Fatalf("code = %d, want 500", code)
	}
	if status != "error" {
		t.Fatalf("status = %q, want error", status)
	}
}

// TestNormalizeSyncPaths 验证 paths 归一化：空表示全量，空白项丢弃，非空项 trim。
func TestNormalizeSyncPaths(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want int
	}{
		{"nil means full sync", nil, 0},
		{"empty strings dropped", []string{"", "  "}, 0},
		{"trimmed and kept", []string{" skills/weather "}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeSyncPaths(c.in)
			if len(got) != c.want {
				t.Fatalf("normalizeSyncPaths(%q) = %v, want %d entries", c.in, got, c.want)
			}
			if c.want == 1 && got[0] != "skills/weather" {
				t.Fatalf("got[0] = %q, want \"skills/weather\"", got[0])
			}
		})
	}
}

// TestSyncHandler_DisabledEndpoints 端到端锁定 SQLite（SyncResource 为 nil）链路：
// resources 传 nil 时三个端点都应返回 409 且 body 含 sync_disabled。
func TestSyncHandler_DisabledEndpoints(t *testing.T) {
	h := NewSyncHandler(t.TempDir(), nil)
	endpoints := []struct {
		name string
		call func(context.Context, *app.RequestContext)
	}{
		{"diff", h.Diff},
		{"push", h.Push},
		{"pull", h.Pull},
	}
	for _, e := range endpoints {
		t.Run(e.name, func(t *testing.T) {
			rc := jsonCtx(consts.MethodPost, `{"paths":[]}`)
			e.call(context.Background(), rc)
			if rc.Response.StatusCode() != 409 {
				t.Fatalf("status = %d, want 409, body=%s", rc.Response.StatusCode(), rc.Response.Body())
			}
			if !strings.Contains(string(rc.Response.Body()), "sync_disabled") {
				t.Errorf("响应体缺少 sync_disabled: %s", rc.Response.Body())
			}
		})
	}
}

// TestSyncHandler_InvalidJSONWriteReturns400 验证写操作对非法 JSON 的严格性：
// Pull 删本地文件、Push 覆盖数据库，body 解析失败必须 400，绝不能静默升级为
// 全量同步。用 nil-repo handler：若错误地继续执行会得到 409 而非 400。
func TestSyncHandler_InvalidJSONWriteReturns400(t *testing.T) {
	h := NewSyncHandler(t.TempDir(), nil)
	endpoints := []struct {
		name string
		call func(context.Context, *app.RequestContext)
	}{
		{"push", h.Push},
		{"pull", h.Pull},
	}
	for _, e := range endpoints {
		t.Run(e.name, func(t *testing.T) {
			// 缺右括号的非法 JSON
			rc := jsonCtx(consts.MethodPost, `{"paths":["skills/x"]`)
			e.call(context.Background(), rc)
			if rc.Response.StatusCode() != 400 {
				t.Fatalf("status = %d, want 400, body=%s", rc.Response.StatusCode(), rc.Response.Body())
			}
			if !strings.Contains(string(rc.Response.Body()), "invalid_request") {
				t.Errorf("响应体缺少 invalid_request: %s", rc.Response.Body())
			}
		})
	}
}

// TestSyncHandler_InvalidJSONDiffLenient 验证只读 Diff 对非法 JSON 保持宽容：
// 视为全量同步继续执行（nil-repo 下即走到 SyncManager 得到 409，而非 400）。
func TestSyncHandler_InvalidJSONDiffLenient(t *testing.T) {
	h := NewSyncHandler(t.TempDir(), nil)
	rc := jsonCtx(consts.MethodPost, `{"paths":["skills/x"]`)
	h.Diff(context.Background(), rc)
	if rc.Response.StatusCode() != 409 {
		t.Fatalf("status = %d, want 409, body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "sync_disabled") {
		t.Errorf("响应体缺少 sync_disabled: %s", rc.Response.Body())
	}
}

// TestSyncHandler_EmptyBodyWriteFullSync 验证空 body 的写操作仍是全量同步
// （API 契约：省略 body 表示全量），nil-repo 下走到 SyncManager 得到 409。
func TestSyncHandler_EmptyBodyWriteFullSync(t *testing.T) {
	h := NewSyncHandler(t.TempDir(), nil)
	rc := jsonCtx(consts.MethodPost, ``)
	h.Push(context.Background(), rc)
	if rc.Response.StatusCode() != 409 {
		t.Fatalf("status = %d, want 409, body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "sync_disabled") {
		t.Errorf("响应体缺少 sync_disabled: %s", rc.Response.Body())
	}
}

// fakeResourceRepo 是内存版 ResourceRepo，仅供端到端测试构造 enabled 的 handler。
type fakeResourceRepo struct {
	res map[string]*repo.Resource
}

func newFakeResourceRepo() *fakeResourceRepo {
	return &fakeResourceRepo{res: map[string]*repo.Resource{}}
}

func (f *fakeResourceRepo) Put(_ context.Context, r *repo.Resource) error {
	f.res[r.Path] = r
	return nil
}

func (f *fakeResourceRepo) Get(_ context.Context, path string) (*repo.Resource, error) {
	if r, ok := f.res[path]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("not found: %s", path)
}

func (f *fakeResourceRepo) Stat(_ context.Context, path string) (*repo.ResourceEntry, error) {
	if r, ok := f.res[path]; ok {
		return &repo.ResourceEntry{Path: r.Path, Size: r.Size, ContentHash: r.ContentHash, UpdatedAt: r.UpdatedAt, Status: r.Status}, nil
	}
	return nil, fmt.Errorf("not found: %s", path)
}

func (f *fakeResourceRepo) List(_ context.Context, prefix string) ([]*repo.ResourceEntry, error) {
	var out []*repo.ResourceEntry
	for _, r := range f.res {
		if r.Status == repo.ResourceStatusActive && strings.HasPrefix(r.Path, prefix) {
			out = append(out, &repo.ResourceEntry{Path: r.Path, Size: r.Size, ContentHash: r.ContentHash, UpdatedAt: r.UpdatedAt, Status: r.Status})
		}
	}
	return out, nil
}

func (f *fakeResourceRepo) ListWithDeleted(_ context.Context, prefix string) ([]*repo.ResourceEntry, error) {
	var out []*repo.ResourceEntry
	for _, r := range f.res {
		if strings.HasPrefix(r.Path, prefix) {
			out = append(out, &repo.ResourceEntry{Path: r.Path, Size: r.Size, ContentHash: r.ContentHash, UpdatedAt: r.UpdatedAt, Status: r.Status})
		}
	}
	return out, nil
}

func (f *fakeResourceRepo) Delete(_ context.Context, path string) error {
	delete(f.res, path)
	return nil
}

// TestSyncHandler_EnabledInvalidPathReturns400 端到端锁定 400 映射：
// enabled 的 handler 收到非法路径时，bindPaths → Diff → ValidateSyncPath →
// syncErrorStatus 全链路应返回 400 invalid_request。
func TestSyncHandler_EnabledInvalidPathReturns400(t *testing.T) {
	h := NewSyncHandler(t.TempDir(), newFakeResourceRepo())
	rc := jsonCtx(consts.MethodPost, `{"paths":["../../etc/passwd"]}`)
	h.Diff(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status = %d, want 400, body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "invalid_request") {
		t.Errorf("响应体缺少 invalid_request: %s", rc.Response.Body())
	}
}
