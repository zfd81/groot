package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// newFilesHandlerForTest 在临时 home 上构造 FilesHandler。
func newFilesHandlerForTest(t *testing.T) (*FilesHandler, string) {
	t.Helper()
	home := t.TempDir()
	for _, p := range []string{"skills/demo", "mcp"} {
		if err := os.MkdirAll(filepath.Join(home, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "GROOT.md"), []byte("memo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("port: 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "groot.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := NewFilesHandler(home)
	if err != nil {
		t.Fatalf("NewFilesHandler: %v", err)
	}
	return h, home
}

// getCtx 构造带 query 参数的 GET 上下文。
func getCtx(query string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/web/files/x?" + query)
	return rc
}

// jsonCtx 构造带 JSON body 的上下文。
func jsonCtx(method, body string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	rc.Request.SetBody([]byte(body))
	return rc
}

// TestFilesHandler_List 验证列表成功响应与 home 字段。
func TestFilesHandler_List(t *testing.T) {
	h, _ := newFilesHandlerForTest(t)
	rc := getCtx("path=")
	h.List(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		Status  string `json:"status"`
		Home    string `json:"home"`
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "success" || resp.Home == "" {
		t.Errorf("resp = %+v", resp)
	}
	for _, e := range resp.Entries {
		if e.Name == "groot.db" {
			t.Error("列表泄露了 groot.db")
		}
	}
}

// TestFilesHandler_ErrorMapping 验证错误码映射：404/403/400。
func TestFilesHandler_ErrorMapping(t *testing.T) {
	h, _ := newFilesHandlerForTest(t)

	rc := getCtx("path=groot.db")
	h.Content(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Errorf("隐藏文件 status = %d, want 404", rc.Response.StatusCode())
	}

	rc = jsonCtx(consts.MethodPut, `{"path":"config.yaml","content":"x"}`)
	h.Save(context.Background(), rc)
	if rc.Response.StatusCode() != 403 {
		t.Errorf("只读保存 status = %d, want 403", rc.Response.StatusCode())
	}

	rc = jsonCtx(consts.MethodPut, `not-json`)
	h.Save(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Errorf("非法 JSON status = %d, want 400", rc.Response.StatusCode())
	}
}

// TestFilesHandler_SaveAndScaffold 验证保存与 scaffold 成功路径。
func TestFilesHandler_SaveAndScaffold(t *testing.T) {
	h, home := newFilesHandlerForTest(t)

	rc := jsonCtx(consts.MethodPut, `{"path":"GROOT.md","content":"new"}`)
	h.Save(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("save status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	data, _ := os.ReadFile(filepath.Join(home, "GROOT.md"))
	if string(data) != "new" {
		t.Errorf("保存未生效: %q", data)
	}

	rc = jsonCtx(consts.MethodPost, `{"kind":"skill","name":"fresh"}`)
	h.Scaffold(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("scaffold status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(rc.Response.Body(), &resp)
	if resp.Path != "skills/fresh/SKILL.md" {
		t.Errorf("scaffold path = %q", resp.Path)
	}
}

// deleteCtx 构造带 query 参数的 DELETE 上下文。
func deleteCtx(query string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodDelete)
	rc.Request.SetRequestURI("/web/files?" + query)
	return rc
}

// multipartCtx 构造 multipart 上传上下文；withPath 为 false 时不写 path 字段。
func multipartCtx(t *testing.T, withPath bool, dir, filename, content string) *app.RequestContext {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if withPath {
		if err := w.WriteField("path", dir); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte(w.FormDataContentType()))
	rc.Request.Header.SetContentLength(buf.Len())
	rc.Request.SetBody(buf.Bytes())
	return rc
}

// TestFilesHandler_Delete 验证删除文件成功、非空目录拒绝与一级目录禁删。
func TestFilesHandler_Delete(t *testing.T) {
	h, home := newFilesHandlerForTest(t)
	target := filepath.Join(home, "skills", "demo", "tmp.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 非空的二级目录 → 409 not_empty
	rc := deleteCtx("path=skills/demo")
	h.Delete(context.Background(), rc)
	if rc.Response.StatusCode() != 409 {
		t.Errorf("非空目录 status = %d, want 409", rc.Response.StatusCode())
	}
	if !strings.Contains(string(rc.Response.Body()), "not_empty") {
		t.Errorf("响应体缺少 not_empty: %s", rc.Response.Body())
	}

	rc = deleteCtx("path=skills/demo/tmp.txt")
	h.Delete(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("delete status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("文件未被删除: %v", err)
	}

	// home 一级目录（即使为空）→ 403 forbidden
	rc = deleteCtx("path=mcp")
	h.Delete(context.Background(), rc)
	if rc.Response.StatusCode() != 403 {
		t.Errorf("一级目录 status = %d, want 403", rc.Response.StatusCode())
	}
	if !strings.Contains(string(rc.Response.Body()), "forbidden") {
		t.Errorf("响应体缺少 forbidden: %s", rc.Response.Body())
	}
}

// TestFilesHandler_Rename 验证改名成功与目标已存在冲突。
func TestFilesHandler_Rename(t *testing.T) {
	h, home := newFilesHandlerForTest(t)

	// 普通文件改名成功（GROOT.md 属结构性条目，禁止改名，见下方 403 断言）
	if err := os.WriteFile(filepath.Join(home, "skills", "demo", "r.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := jsonCtx(consts.MethodPost, `{"from":"skills/demo/r.txt","to":"skills/demo/r2.txt"}`)
	h.Rename(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("rename status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "demo", "r2.txt")); err != nil {
		t.Errorf("改名未生效: %v", err)
	}

	// GROOT.md 与一级目录禁止改名
	rc = jsonCtx(consts.MethodPost, `{"from":"GROOT.md","to":"GROOT2.md"}`)
	h.Rename(context.Background(), rc)
	if rc.Response.StatusCode() != 403 {
		t.Errorf("改名 GROOT.md status = %d, want 403", rc.Response.StatusCode())
	}
	rc = jsonCtx(consts.MethodPost, `{"from":"mcp","to":"mcp2"}`)
	h.Rename(context.Background(), rc)
	if rc.Response.StatusCode() != 403 {
		t.Errorf("改名一级目录 status = %d, want 403", rc.Response.StatusCode())
	}

	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(home, "skills", "demo", name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rc = jsonCtx(consts.MethodPost, `{"from":"skills/demo/a.txt","to":"skills/demo/b.txt"}`)
	h.Rename(context.Background(), rc)
	if rc.Response.StatusCode() != 409 {
		t.Errorf("目标已存在 status = %d, want 409", rc.Response.StatusCode())
	}
	if !strings.Contains(string(rc.Response.Body()), "exists") {
		t.Errorf("响应体缺少 exists: %s", rc.Response.Body())
	}
}

// TestFilesHandler_Upload 验证上传：缺 file 字段 400、home 根 200、子目录 200。
func TestFilesHandler_Upload(t *testing.T) {
	h, home := newFilesHandlerForTest(t)

	rc := jsonCtx(consts.MethodPost, `{}`)
	h.Upload(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Errorf("缺 file 字段 status = %d, want 400", rc.Response.StatusCode())
	}

	rc = multipartCtx(t, true, "", "root.txt", "data")
	h.Upload(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Errorf("home 根上传 status = %d, want 200", rc.Response.StatusCode())
	}

	rc = multipartCtx(t, true, "mcp", "up.txt", "data")
	h.Upload(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("upload status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	data, err := os.ReadFile(filepath.Join(home, "mcp", "up.txt"))
	if err != nil || string(data) != "data" {
		t.Errorf("上传内容不对: %q err=%v", data, err)
	}
}

// TestFilesHandler_Download 验证下载成功响应头与 body、目录返回 404。
func TestFilesHandler_Download(t *testing.T) {
	h, _ := newFilesHandlerForTest(t)

	rc := getCtx("path=GROOT.md")
	h.Download(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("download status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if cd := rc.Response.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if body := string(rc.Response.Body()); body != "memo" {
		t.Errorf("body = %q, want memo", body)
	}

	rc = getCtx("path=skills")
	h.Download(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Errorf("目录下载 status = %d, want 404", rc.Response.StatusCode())
	}
}
