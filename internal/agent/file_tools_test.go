package agent

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/storage"
)

// newFileToolsTestEnv 构造一个隔离的临时记忆目录 + Manager + local storage,
// 返回工具构造所需的全部材料。
func newFileToolsTestEnv(t *testing.T, sessionID string) (*memory.Manager, storage.Storage, string) {
	t.Helper()
	tmpDir := t.TempDir()
	store := storage.NewLocal()
	mgr := memory.NewManager(tmpDir, 7, logger.NewNop(), store)
	if err := mgr.CreateSession(sessionID); err != nil {
		t.Fatalf("CreateSession() 失败: %v", err)
	}
	return mgr, store, tmpDir
}

// writeAttachment 在 attachments 目录下落一份测试附件。
func writeAttachment(t *testing.T, mgr *memory.Manager, sessionID, name string, content []byte) {
	t.Helper()
	dir := mgr.AttachmentsDir(sessionID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir attachments: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), content, 0644); err != nil {
		t.Fatalf("write attachment %s: %v", name, err)
	}
}

func TestGrootFileListTool_EmptyAttachmentsDir(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	tool := NewGrootFileListTool(store, mgr, "s1")

	got, err := tool.InvokableRun(context.Background(), "")
	if err != nil {
		t.Fatalf("InvokableRun() err = %v", err)
	}
	if !strings.Contains(got, "无附件") {
		t.Errorf("空目录应返回提示文本，got: %q", got)
	}
}

func TestGrootFileListTool_RendersTableAndFiltersDir(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	writeAttachment(t, mgr, "s1", "report.md", []byte("# hello"))
	writeAttachment(t, mgr, "s1", "logo.png", []byte{0x89, 0x50, 0x4e, 0x47})
	// 子目录应被过滤
	if err := os.MkdirAll(filepath.Join(mgr.AttachmentsDir("s1"), "subdir"), 0755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	tool := NewGrootFileListTool(store, mgr, "s1")
	got, err := tool.InvokableRun(context.Background(), "")
	if err != nil {
		t.Fatalf("InvokableRun() err = %v", err)
	}
	if !strings.Contains(got, "report.md") || !strings.Contains(got, "logo.png") {
		t.Errorf("输出缺失文件名: %s", got)
	}
	if strings.Contains(got, "subdir") {
		t.Errorf("子目录不应出现在清单中: %s", got)
	}
	if !strings.Contains(got, "| 文件名 | 大小 | 上传时间 |") {
		t.Errorf("输出缺失 Markdown 表头: %s", got)
	}
}

func TestGrootFileReadTool_TextFileReturnsRaw(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	cases := []struct {
		name    string
		content string
	}{
		{"a.md", "# Hello\n"},
		{"b.json", `{"k":"v"}`},
		{"c.go", "package main\n"},
	}
	for _, c := range cases {
		writeAttachment(t, mgr, "s1", c.name, []byte(c.content))
	}

	tool := NewGrootFileReadTool(store, mgr, "s1")
	for _, c := range cases {
		got, err := tool.InvokableRun(context.Background(), `{"filename":"`+c.name+`"}`)
		if err != nil {
			t.Fatalf("read %s err = %v", c.name, err)
		}
		if got != c.content {
			t.Errorf("read %s = %q, want %q", c.name, got, c.content)
		}
	}
}

func TestGrootFileReadTool_BinaryFileReturnsBase64(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	raw := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	writeAttachment(t, mgr, "s1", "logo.png", raw)

	tool := NewGrootFileReadTool(store, mgr, "s1")
	got, err := tool.InvokableRun(context.Background(), `{"filename":"logo.png"}`)
	if err != nil {
		t.Fatalf("InvokableRun() err = %v", err)
	}
	if got != base64.StdEncoding.EncodeToString(raw) {
		t.Errorf("二进制文件应返回 base64 编码，got: %q", got)
	}
}

func TestGrootFileReadTool_MissingFile(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	tool := NewGrootFileReadTool(store, mgr, "s1")
	_, err := tool.InvokableRun(context.Background(), `{"filename":"missing.txt"}`)
	if err == nil {
		t.Fatal("文件不存在应返回 error")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("error 信息缺失: %v", err)
	}
}

func TestGrootFileReadTool_RejectsPathInjection(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	tool := NewGrootFileReadTool(store, mgr, "s1")

	cases := []string{
		`{"filename":"../etc/passwd"}`,
		`{"filename":"a/b.txt"}`,
		`{"filename":"..\\evil"}`,
		`{"filename":"/abs/path"}`,
		`{"filename":""}`,
	}
	for _, args := range cases {
		_, err := tool.InvokableRun(context.Background(), args)
		if err == nil {
			t.Errorf("非法 filename 应返回 error，args=%s", args)
		}
	}
}

func TestGrootFileReadTool_InvalidJSON(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	tool := NewGrootFileReadTool(store, mgr, "s1")
	_, err := tool.InvokableRun(context.Background(), `{not json`)
	if err == nil {
		t.Fatal("非法 JSON 应返回 error")
	}
}

// TestFileTools_SessionIsolation 验证两个工具实例分别绑定不同 sessionID 后,
// LLM 无法跨会话访问其他 session 的附件。
func TestFileTools_SessionIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	store := storage.NewLocal()
	mgr := memory.NewManager(tmpDir, 7, logger.NewNop(), store)
	if err := mgr.CreateSession("a"); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := mgr.CreateSession("b"); err != nil {
		t.Fatalf("create b: %v", err)
	}
	writeAttachment(t, mgr, "a", "only-in-a.md", []byte("from A"))

	listB := NewGrootFileListTool(store, mgr, "b")
	listOut, err := listB.InvokableRun(context.Background(), "")
	if err != nil {
		t.Fatalf("list b err: %v", err)
	}
	if strings.Contains(listOut, "only-in-a.md") {
		t.Errorf("session b 的 list 不应看到 a 的附件: %s", listOut)
	}

	readB := NewGrootFileReadTool(store, mgr, "b")
	if _, err := readB.InvokableRun(context.Background(), `{"filename":"only-in-a.md"}`); err == nil {
		t.Error("session b 不应能读到 a 的附件")
	}
}

// TestFileTools_PathReusesAttachmentsDir 验证工具内部通过 Manager.AttachmentsDir
// 拼路径,与 Manager 自身的拼接规则保持一致。
func TestFileTools_PathReusesAttachmentsDir(t *testing.T) {
	mgr, store, _ := newFileToolsTestEnv(t, "s1")
	writeAttachment(t, mgr, "s1", "a.txt", []byte("hi"))

	// list 输出应只显示文件 base name(同 Manager 给磁盘上的命名)
	listOut, err := NewGrootFileListTool(store, mgr, "s1").InvokableRun(context.Background(), "")
	if err != nil {
		t.Fatalf("list err: %v", err)
	}
	if !strings.Contains(listOut, "| a.txt |") {
		t.Errorf("list 输出应通过 base name 展示文件: %s", listOut)
	}

	// read 应能命中,且工具实际打开的就是 Manager.AttachmentsDir 拼出的路径。
	got, err := NewGrootFileReadTool(store, mgr, "s1").InvokableRun(context.Background(), `{"filename":"a.txt"}`)
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if got != "hi" {
		t.Errorf("read = %q, want %q", got, "hi")
	}
}

func TestIsTextFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"a.md", true},
		{"b.JSON", true}, // 大小写无关
		{"c.go", true},
		{"d.png", false},
		{"e.bin", false},
		{"Makefile", false}, // 无扩展名一律按二进制
		{"", false},
	}
	for _, c := range cases {
		if got := isTextFile(c.name); got != c.want {
			t.Errorf("isTextFile(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
