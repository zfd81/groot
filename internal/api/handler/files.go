package handler

import (
	"context"
	"errors"
	"net/url"
	"os"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/webfiles"
)

// FilesHandler 文件面板的 HTTP 绑定层；安全与业务规则在 internal/webfiles。
type FilesHandler struct {
	svc *webfiles.Service
}

// NewFilesHandler 构造 FilesHandler；homeDir 无法解析时返回错误（调用方决定降级）。
func NewFilesHandler(homeDir string) (*FilesHandler, error) {
	svc, err := webfiles.NewService(homeDir)
	if err != nil {
		return nil, err
	}
	return &FilesHandler{svc: svc}, nil
}

// writeFilesError 把 webfiles 错误映射为 HTTP 响应。
func writeFilesError(rc *app.RequestContext, err error) {
	switch {
	case errors.Is(err, webfiles.ErrNotFound):
		rc.JSON(404, utils.H{"status": "not_found", "message": "文件或目录不存在"})
	case errors.Is(err, webfiles.ErrReadOnly):
		rc.JSON(403, utils.H{"status": "read_only", "message": "该文件为只读"})
	case errors.Is(err, webfiles.ErrForbidden):
		rc.JSON(403, utils.H{"status": "forbidden", "message": "该位置不允许此操作"})
	case errors.Is(err, webfiles.ErrExists):
		rc.JSON(409, utils.H{"status": "exists", "message": "同名文件或目录已存在"})
	case errors.Is(err, webfiles.ErrNotEmpty):
		rc.JSON(409, utils.H{"status": "not_empty", "message": "目录非空，无法删除"})
	case errors.Is(err, webfiles.ErrTooLarge):
		rc.JSON(413, utils.H{"status": "too_large", "message": "内容超出大小限制"})
	case errors.Is(err, webfiles.ErrInvalid):
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "参数非法"})
	default:
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
	}
}

// List 处理 GET /web/files/list?path=
func (h *FilesHandler) List(ctx context.Context, rc *app.RequestContext) {
	rel := rc.Query("path")
	entries, err := h.svc.List(rel)
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success", "path": rel, "home": h.svc.Home(), "entries": entries})
}

// Content 处理 GET /web/files/content?path=
func (h *FilesHandler) Content(ctx context.Context, rc *app.RequestContext) {
	fc, err := h.svc.Read(rc.Query("path"))
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{
		"status": "success", "path": fc.Path,
		"readonly": fc.Readonly, "binary": fc.Binary, "content": fc.Content,
	})
}

// Save 处理 PUT /web/files/content
func (h *FilesHandler) Save(ctx context.Context, rc *app.RequestContext) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Write(req.Path, req.Content); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// pathReq 只含 path 字段的请求体。
type pathReq struct {
	Path string `json:"path"`
}

// Mkdir 处理 POST /web/files/mkdir
func (h *FilesHandler) Mkdir(ctx context.Context, rc *app.RequestContext) {
	var req pathReq
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Mkdir(req.Path); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Create 处理 POST /web/files/create
func (h *FilesHandler) Create(ctx context.Context, rc *app.RequestContext) {
	var req pathReq
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Create(req.Path); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Rename 处理 POST /web/files/rename
func (h *FilesHandler) Rename(ctx context.Context, rc *app.RequestContext) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Rename(req.From, req.To); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Delete 处理 DELETE /web/files?path=
func (h *FilesHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	if err := h.svc.Delete(rc.Query("path")); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Upload 处理 POST /web/files/upload（multipart：path=目标目录, file=文件）
func (h *FilesHandler) Upload(ctx context.Context, rc *app.RequestContext) {
	fh, err := rc.FormFile("file")
	if err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "缺少 file 字段"})
		return
	}
	dst, err := h.svc.UploadTarget(rc.PostForm("path"), fh.Filename, fh.Size)
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	if err := rc.SaveUploadedFile(fh, dst); err != nil {
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Download 处理 GET /web/files/download?path=
func (h *FilesHandler) Download(ctx context.Context, rc *app.RequestContext) {
	abs, name, err := h.svc.DownloadPath(rc.Query("path"))
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	// 校验与响应之间文件可能被删（竞态），自己打开文件以保证错误响应形态一致
	f, err := os.Open(abs)
	if err != nil {
		writeFilesError(rc, webfiles.ErrNotFound)
		return
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		writeFilesError(rc, webfiles.ErrNotFound)
		return
	}
	rc.Response.Header.Set("Content-Disposition",
		`attachment; filename*=UTF-8''`+url.PathEscape(name))
	rc.SetContentType("application/octet-stream")
	// f 由 hertz 在响应体读完后关闭（SetBodyStream 对 io.Closer 的约定），不能提前 Close
	rc.SetBodyStream(f, int(info.Size()))
}

// Scaffold 处理 POST /web/files/scaffold
func (h *FilesHandler) Scaffold(ctx context.Context, rc *app.RequestContext) {
	var req struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	rel, err := h.svc.Scaffold(req.Kind, req.Name)
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success", "path": rel})
}
