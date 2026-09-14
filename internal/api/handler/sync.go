package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/repo"
	grootsync "github.com/zfd81/groot/internal/sync"
)

// SyncHandler 配置同步的 HTTP 绑定层；同步语义在 internal/sync。
type SyncHandler struct {
	mgr grootsync.SyncManager
}

// NewSyncHandler 构造 SyncHandler。resources 为 nil（SQLite 单机模式，
// 取 repofactory.Repos.SyncResource）时 SyncManager 的所有方法返回
// ErrSyncDisabled，端点统一回 409。
func NewSyncHandler(homeDir string, resources repo.ResourceRepo) *SyncHandler {
	return &SyncHandler{mgr: grootsync.NewSyncManager(homeDir, resources)}
}

// syncRequest 是三个端点共用的请求体。paths 省略或为空表示全量同步。
type syncRequest struct {
	Paths []string `json:"paths"`
}

// normalizeSyncPaths 去掉空白项并 trim；结果为空表示全量（交给 SyncManager 展开白名单）。
func normalizeSyncPaths(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// syncErrorStatus 把同步错误映射为 HTTP 状态码与业务 status 值。
//
// 两个分支都是哨兵匹配（errors.Is，可穿透 %w 包裹），因此顺序不构成正确性
// 约束；保持 disabled 在前只是习惯——它是"功能整体不可用"，语义上先于
// "单次请求参数非法"。历史上第二个分支曾是 HasPrefix("sync: ") 字符串匹配，
// 而 ErrSyncDisabled 的消息恰好也以 "sync: " 开头，那时顺序不可交换。
func syncErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, grootsync.ErrSyncDisabled):
		return 409, "sync_disabled"
	case errors.Is(err, grootsync.ErrInvalidPath):
		return 400, "invalid_request"
	default:
		return 500, "error"
	}
}

func writeSyncError(rc *app.RequestContext, err error) {
	code, status := syncErrorStatus(err)
	message := "内部错误"
	switch status {
	case "sync_disabled":
		// 不直接用 err.Error()：ErrSyncDisabled 的消息带"请在 env.yaml 中配置
		// database 节"的 CLI 提示，对浏览器用户无意义，Web 场景只说结论。
		message = "配置同步仅在 MySQL/PostgreSQL 模式下可用"
	case "invalid_request":
		message = err.Error()
	}
	rc.JSON(code, utils.H{"status": status, "message": message})
}

// bindPaths 解析请求体中的 paths；请求体缺失或非法 JSON 视为全量同步。
// 仅供只读的 Diff 使用——宽容解析无害。写操作用 bindPathsStrict。
func bindPaths(rc *app.RequestContext) []string {
	var req syncRequest
	if err := rc.BindJSON(&req); err != nil {
		return nil
	}
	return normalizeSyncPaths(req.Paths)
}

// bindPathsStrict 是 Push/Pull 用的严格版：空 body 仍视为全量（API 契约），
// 但非法 JSON 直接回 400——Pull 删本地文件、Push 覆盖集群共享数据库，
// 作用域绝不能因 body 解析失败而从"指定路径"静默升级为"全部白名单资源"。
// 第二个返回值为 false 表示已写出错误响应，调用方应直接 return。
func bindPathsStrict(rc *app.RequestContext) ([]string, bool) {
	if len(rc.Request.Body()) == 0 {
		return nil, true
	}
	var req syncRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return nil, false
	}
	return normalizeSyncPaths(req.Paths), true
}

// Diff 处理 POST /web/sync/diff —— 只读比较，不修改任何一侧。
// 用 POST 而非 GET 是为了在请求体里传路径数组。
func (h *SyncHandler) Diff(ctx context.Context, rc *app.RequestContext) {
	paths := bindPaths(rc)
	// 清理上次中断留下的 *.tmp 残留，确保差异反映真实状态
	_ = h.mgr.CleanTmpResidue(paths)

	d, err := h.mgr.Diff(paths)
	if err != nil {
		writeSyncError(rc, err)
		return
	}
	view := grootsync.BuildWebDiff(d)
	rc.JSON(200, utils.H{
		"status":       "success",
		"inSync":       view.InSync,
		"needsRestart": view.NeedsRestart,
		"entries":      view.Entries,
	})
}

// Push 处理 POST /web/sync/push —— 本地 HOME 覆盖数据库。
// 不接受前端传来的差异快照：SyncManager.Push 内部会重新计算差异后执行，
// 因此用户确认时以那一刻的真实差异为准，服务端无需保存状态。
func (h *SyncHandler) Push(ctx context.Context, rc *app.RequestContext) {
	h.apply(rc, h.mgr.Push)
}

// Pull 处理 POST /web/sync/pull —— 数据库覆盖本地 HOME。
func (h *SyncHandler) Pull(ctx context.Context, rc *app.RequestContext) {
	h.apply(rc, h.mgr.Pull)
}

func (h *SyncHandler) apply(rc *app.RequestContext, op func([]string) error) {
	paths, ok := bindPathsStrict(rc)
	if !ok {
		return
	}
	if err := op(paths); err != nil {
		writeSyncError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}
