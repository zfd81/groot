package api

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func serveUI(t *testing.T, fsys fstest.MapFS, uri string) *app.RequestContext {
	t.Helper()
	h := NewWebUIHandler(fsys)
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI(uri)
	h(context.Background(), rc)
	return rc
}

var builtFS = fstest.MapFS{
	"index.html":    {Data: []byte("<html>groot</html>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

// TestWebUI_ServeIndex /ui/ 返回 index.html。
func TestWebUI_ServeIndex(t *testing.T) {
	rc := serveUI(t, builtFS, "/ui/")
	if rc.Response.StatusCode() != 200 || !strings.Contains(string(rc.Response.Body()), "groot") {
		t.Fatalf("unexpected: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if ct := string(rc.Response.Header.ContentType()); !strings.Contains(ct, "text/html") {
		t.Errorf("content type should be html, got %s", ct)
	}
}

// TestWebUI_ServeAsset 静态资源按扩展名返回正确 Content-Type。
func TestWebUI_ServeAsset(t *testing.T) {
	rc := serveUI(t, builtFS, "/ui/assets/app.js")
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
	}
	if ct := string(rc.Response.Header.ContentType()); !strings.Contains(ct, "javascript") {
		t.Errorf("content type should be javascript, got %s", ct)
	}
}

// TestWebUI_HistoryFallback 未知路径回退 index.html（前端 history 路由）。
func TestWebUI_HistoryFallback(t *testing.T) {
	rc := serveUI(t, builtFS, "/ui/dashboard")
	if rc.Response.StatusCode() != 200 || !strings.Contains(string(rc.Response.Body()), "groot") {
		t.Fatalf("history fallback failed: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// TestWebUI_NotBuilt dist 中无 index.html 时返回提示页。
func TestWebUI_NotBuilt(t *testing.T) {
	rc := serveUI(t, fstest.MapFS{".gitkeep": {Data: []byte("")}}, "/ui/")
	if rc.Response.StatusCode() != 200 || !strings.Contains(string(rc.Response.Body()), "未构建") {
		t.Fatalf("expected placeholder page, got: %d %s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// TestWebUI_MissingAsset404 带扩展名的资源未命中返回 404，而非 HTML index，
// 避免浏览器把 text/html 当作脚本静默失败。
func TestWebUI_MissingAsset404(t *testing.T) {
	rc := serveUI(t, builtFS, "/ui/assets/missing.js")
	if rc.Response.StatusCode() != 404 {
		t.Fatalf("expected 404 for missing asset, got %d", rc.Response.StatusCode())
	}
	if strings.Contains(string(rc.Response.Body()), "groot") {
		t.Errorf("missing asset should not fall back to index html")
	}
}

// TestWebUI_AssetCacheHeader 构建产物（assets/ 下）带长期强缓存头。
func TestWebUI_AssetCacheHeader(t *testing.T) {
	rc := serveUI(t, builtFS, "/ui/assets/app.js")
	if cc := string(rc.Response.Header.Peek("Cache-Control")); !strings.Contains(cc, "immutable") {
		t.Errorf("expected immutable cache header, got %q", cc)
	}
}

// TestWebUI_NoPathTraversal 含 .. 的路径不得读出 dist 之外的文件，
// 一律回退 index.html（200）或对疑似资源路径返回 404，绝不泄漏文件内容。
func TestWebUI_NoPathTraversal(t *testing.T) {
	secretFS := fstest.MapFS{
		"index.html": {Data: []byte("<html>groot</html>")},
		"passwd":     {Data: []byte("root:x:0:0")},
	}
	for _, uri := range []string{
		"/ui/../../etc/passwd",
		"/ui/..%2f..%2fetc%2fpasswd",
		"/ui/assets/../../passwd",
		"/ui//etc/passwd",
	} {
		rc := serveUI(t, secretFS, uri)
		body := string(rc.Response.Body())
		if strings.Contains(body, "root:x:0:0") {
			t.Errorf("%s leaked file outside dist: %s", uri, body)
		}
		if code := rc.Response.StatusCode(); code != 200 && code != 404 {
			t.Errorf("%s: expected 200 fallback or 404, got %d", uri, code)
		}
	}
}
