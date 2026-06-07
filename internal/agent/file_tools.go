// Package agent 内置文件工具：groot_file_list / groot_file_read。
//
// 这两个工具让 LLM 能直接列出和读取当前会话的附件。它们以请求级生命周期
// 实例化——每次 Executor.Execute 在创建 engine 前现场构造一对，把本次执行
// 的 sessionID 写入实例字段。LLM 入参中不暴露 session 信息，跨会话访问被
// 字段值天然隔离。
//
// 后端不感知：storage.Storage 在启动期由 storage.New(cfg.Storage) 选定
// （local 或 minio），运行期不再切换；工具直接调用接口方法。
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/storage"
)

// textExtensions 列出按文件扩展名（小写）判定为 UTF-8 文本的后缀集合。
// 命中则 groot_file_read 直接返回原文，否则按二进制 base64 编码返回。
var textExtensions = map[string]struct{}{
	".txt": {}, ".md": {}, ".markdown": {},
	".json": {}, ".yaml": {}, ".yml": {}, ".toml": {}, ".xml": {},
	".html": {}, ".htm": {}, ".css": {},
	".csv": {}, ".tsv": {},
	".log": {}, ".ini": {}, ".conf": {},
	".go": {}, ".py": {}, ".js": {}, ".ts": {}, ".tsx": {}, ".jsx": {},
	".java": {}, ".c": {}, ".cpp": {}, ".h": {}, ".hpp": {},
	".rs": {}, ".rb": {}, ".php": {}, ".sh": {}, ".bash": {}, ".zsh": {},
	".sql": {},
}

// isTextFile 按扩展名（小写）判定文本/二进制。无扩展名一律视为二进制。
func isTextFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return false
	}
	_, ok := textExtensions[ext]
	return ok
}

// validateFilename 校验 LLM 传入的 filename 合法性：仅接受裸文件名，
// 拒绝路径分隔符（/、\）与路径回溯（..），同时拒绝空串与绝对路径。
func validateFilename(name string) error {
	if name == "" {
		return errors.New("filename 不能为空")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("filename 不能包含路径分隔符: %q", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("filename 不能包含路径回溯: %q", name)
	}
	return nil
}

// GrootFileListTool 列出当前会话附件清单。请求级实例。
type GrootFileListTool struct {
	storage   storage.Storage
	memory    *memory.Manager
	sessionID string
}

// NewGrootFileListTool 构造请求级 list 工具。
// storage / memory 为进程级共享，sessionID 是本次 Execute 绑定的会话 ID。
func NewGrootFileListTool(store storage.Storage, mem *memory.Manager, sessionID string) *GrootFileListTool {
	return &GrootFileListTool{
		storage:   store,
		memory:    mem,
		sessionID: sessionID,
	}
}

// Info 满足 tool.InvokableTool。
func (t *GrootFileListTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name:        "groot_file_list",
		Desc:        "列出当前会话的附件清单，返回 Markdown 表格（列：文件名 / 大小 / 上传时间）；无附件时返回文本提示。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
	}, nil
}

// InvokableRun 列出 attachments 目录下的直接子文件（过滤掉子目录），渲染为
// Markdown 表格返回。目录不存在或为空时返回提示文本。
func (t *GrootFileListTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	dir := t.memory.AttachmentsDir(t.sessionID)

	infos, err := t.storage.List(ctx, dir)
	if err != nil {
		// 目录不存在视同空目录——会话刚创建、尚未上传任何附件时的常态。
		if errors.Is(err, storage.ErrNotFound) {
			return "当前会话无附件。", nil
		}
		return "", fmt.Errorf("列出附件失败: %w", err)
	}

	files := make([]*storage.FileInfo, 0, len(infos))
	for _, fi := range infos {
		if fi.IsDir {
			continue
		}
		files = append(files, fi)
	}
	if len(files) == 0 {
		return "当前会话无附件。", nil
	}

	// 按文件名字典序排列，输出稳定。
	sort.Slice(files, func(i, j int) bool {
		return filepath.Base(files[i].Path) < filepath.Base(files[j].Path)
	})

	var sb strings.Builder
	sb.WriteString("| 文件名 | 大小 | 上传时间 |\n")
	sb.WriteString("|--------|------|----------|\n")
	for _, fi := range files {
		fmt.Fprintf(&sb, "| %s | %s | %s |\n",
			filepath.Base(fi.Path),
			formatSize(fi.Size),
			fi.ModTime.Format("2006-01-02 15:04:05"),
		)
	}
	return sb.String(), nil
}

// formatSize 把字节数渲染为人类可读字符串（B / KB / MB / GB）。
func formatSize(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.2f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.2f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// GrootFileReadArgument groot_file_read 入参。
type GrootFileReadArgument struct {
	Filename string `json:"filename"`
}

// GrootFileReadTool 按文件名读取当前会话附件内容。请求级实例。
type GrootFileReadTool struct {
	storage   storage.Storage
	memory    *memory.Manager
	sessionID string
}

// NewGrootFileReadTool 构造请求级 read 工具。参数语义同 NewGrootFileListTool。
func NewGrootFileReadTool(store storage.Storage, mem *memory.Manager, sessionID string) *GrootFileReadTool {
	return &GrootFileReadTool{
		storage:   store,
		memory:    mem,
		sessionID: sessionID,
	}
}

// Info 满足 tool.InvokableTool。
func (t *GrootFileReadTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "groot_file_read",
		Desc: "按文件名读取当前会话的附件内容：文本文件返回 UTF-8 原文，二进制文件返回 base64 字符串。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"filename": {
				Desc:     "附件文件名，不含路径分隔符（/、\\）和路径回溯（..）",
				Required: true,
				Type:     schema.String,
			},
		}),
	}, nil
}

// InvokableRun 读取指定附件内容。文本类按 UTF-8 直出，其余按 base64 编码。
// 文件不存在、参数非法、底层 IO 异常等均通过 error 返回值呈现。
func (t *GrootFileReadTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var input GrootFileReadArgument
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("解析 groot_file_read 参数失败: %w", err)
	}
	if err := validateFilename(input.Filename); err != nil {
		return "", err
	}

	fullPath := filepath.Join(t.memory.AttachmentsDir(t.sessionID), input.Filename)
	rc, err := t.storage.Read(ctx, fullPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", fmt.Errorf("附件不存在: %s", input.Filename)
		}
		return "", fmt.Errorf("读取附件失败: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("读取附件内容失败: %w", err)
	}

	if isTextFile(input.Filename) {
		return string(data), nil
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
