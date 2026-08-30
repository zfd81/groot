package api

import (
	"context"
	"io/fs"
	"mime"
	"path"
	"path/filepath"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/zfd81/groot/web"
)

const webUIPlaceholder = `<!DOCTYPE html>
<html lang="zh"><head><meta charset="utf-8"><title>Groot</title></head>
<body style="font-family:sans-serif;padding:40px">
<h2>Groot Web UI 未构建</h2>
<p>请在项目根目录执行 <code>make web</code> 后重新编译 groot。</p>
</body></html>`

// serveIndexOrPlaceholder 回退到 index.html；连 index.html 都不存在时返回未构建提示页。
func serveIndexOrPlaceholder(fsys fs.FS, rc *app.RequestContext) {
	data, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		rc.SetContentType("text/html; charset=utf-8")
		rc.SetStatusCode(200)
		rc.WriteString(webUIPlaceholder)
		return
	}
	rc.SetContentType("text/html; charset=utf-8")
	rc.SetStatusCode(200)
	rc.Write(data)
}

// NewWebUIHandler 返回服务 /ui/* 的处理函数。
// fsys 以 dist 为根；未命中的路径回退 index.html（支持前端 history 路由），
// 连 index.html 都不存在时返回未构建提示页。
//
// 安全：请求路径经规范化后用 fs.ValidPath 校验，任何含 ".." 或非法形式的
// 路径都不会命中文件读取，一律走 index.html 回退，杜绝目录穿越。
func NewWebUIHandler(fsys fs.FS) app.HandlerFunc {
	return func(ctx context.Context, rc *app.RequestContext) {
		// Hertz 在解析时已折叠路径中的 ".."：形如 /ui/../../etc/passwd
		// 会被规范化成 /etc/passwd，/ui 前缀被吃掉。因此穿越攻击的判据
		// 就是——规范化后的路径不再落在 /ui 命名空间内，此时一律回退。
		raw := string(rc.URI().Path())
		if raw != "/ui" && !strings.HasPrefix(raw, "/ui/") {
			serveIndexOrPlaceholder(fsys, rc)
			return
		}
		p := strings.TrimPrefix(raw, "/ui")
		p = strings.TrimPrefix(p, "/")
		if p == "" {
			serveIndexOrPlaceholder(fsys, rc)
			return
		}
		// 二次保险：规范化后仍非法（含 .. 或非 fs.ValidPath 形式）则回退。
		if p != path.Clean(p) || !fs.ValidPath(p) {
			serveIndexOrPlaceholder(fsys, rc)
			return
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			// 带扩展名的路径（如 .js/.css/.png）视为静态资源：未命中直接 404，
			// 避免把 text/html 的 index 当作脚本返回而让浏览器静默失败。
			// 无扩展名的路径才走 index 回退（支持前端 history 路由）。
			if filepath.Ext(p) != "" {
				rc.SetStatusCode(404)
				return
			}
			serveIndexOrPlaceholder(fsys, rc)
			return
		}
		ct := mime.TypeByExtension(filepath.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		// 构建产物文件名带内容哈希，可长期强缓存；其余按需。
		if strings.HasPrefix(p, "assets/") {
			rc.Response.Header.Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		rc.SetContentType(ct)
		rc.SetStatusCode(200)
		rc.Write(data)
	}
}

// RegisterWebUI 在 Hertz 上注册 Web 界面静态路由。
func RegisterWebUI(h *server.Hertz) {
	dist, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		return // embed 缺失时不注册（理论上不会发生）
	}
	handler := NewWebUIHandler(dist)
	h.GET("/ui", handler)
	h.GET("/ui/*filepath", handler)
}
