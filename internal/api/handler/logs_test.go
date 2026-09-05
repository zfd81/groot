package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// newLogsHandlerForTest 构造指向临时日志目录的 LogsHandler。
func newLogsHandlerForTest(dir string) *LogsHandler {
	return NewLogsHandler(config.LoggingConfig{
		File: config.LogFileConfig{Directory: dir, FilenamePattern: "groot-{date}.log"},
	})
}

// serveLogs 构造 GET 请求（可选设置 :sid 路径参数）并执行 handler。
func serveLogs(h *LogsHandler, sid string, withParam bool) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	if withParam {
		rc.Params = append(rc.Params, param.Param{Key: "sid", Value: sid})
	}
	h.Serve(context.Background(), rc)
	return rc
}

// logsResponse /web/logs/:sid 的响应结构（仅测试用）。
type logsResponse struct {
	Status    string            `json:"status"`
	SessionID string            `json:"session_id"`
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated"`
	Logs      []logger.LogEntry `json:"logs"`
}

// decodeLogs 解析 200 响应体，非 200 直接失败。
func decodeLogs(t *testing.T, rc *app.RequestContext) logsResponse {
	t.Helper()
	if got := rc.Response.StatusCode(); got != 200 {
		t.Fatalf("expected 200, got %d body=%s", got, rc.Response.Body())
	}
	var resp logsResponse
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rc.Response.Body())
	}
	return resp
}

// writeTodayLogFile 在 dir 下写入今天日期的日志文件，内容为给定的若干行。
func writeTodayLogFile(t *testing.T, dir string, lines []string) {
	t.Helper()
	name := "groot-" + time.Now().Format("2006-01-02") + ".log"
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("写日志文件失败: %v", err)
	}
}

// TestLogsHandler_Normal 验证正常查询：只返回目标会话的条目，字段正确。
func TestLogsHandler_Normal(t *testing.T) {
	dir := t.TempDir()
	writeTodayLogFile(t, dir, []string{
		`{"timestamp":"2026-09-05T10:00:00","level":"info","message":"start","caller":"a.go:1","session_id":"sess_a","tool":"web_search"}`,
		`{"timestamp":"2026-09-05T10:00:01","level":"error","message":"boom","caller":"b.go:2","session_id":"sess_b"}`,
		`{"timestamp":"2026-09-05T10:00:02","level":"error","message":"failed","caller":"a.go:3","session_id":"sess_a"}`,
	})

	rc := serveLogs(newLogsHandlerForTest(dir), "sess_a", true)
	resp := decodeLogs(t, rc)

	if resp.Status != "success" {
		t.Errorf("status = %s, want success", resp.Status)
	}
	if resp.SessionID != "sess_a" {
		t.Errorf("session_id = %s, want sess_a", resp.SessionID)
	}
	if resp.Count != 2 || len(resp.Logs) != 2 {
		t.Fatalf("count = %d, len(logs) = %d, want 2/2; body=%s", resp.Count, len(resp.Logs), rc.Response.Body())
	}
	if resp.Truncated {
		t.Errorf("truncated = true, want false")
	}
	if resp.Logs[0].Message != "start" || resp.Logs[1].Message != "failed" {
		t.Errorf("logs 内容或顺序不对: %+v", resp.Logs)
	}
	if resp.Logs[0].Level != "info" || resp.Logs[0].Caller != "a.go:1" {
		t.Errorf("logs[0] 字段不对: %+v", resp.Logs[0])
	}
	if got, _ := resp.Logs[0].Fields["tool"].(string); got != "web_search" {
		t.Errorf("logs[0].fields.tool = %v, want web_search", resp.Logs[0].Fields["tool"])
	}
	for _, e := range resp.Logs {
		if e.Message == "boom" {
			t.Errorf("包含了其他会话的日志: %+v", e)
		}
	}
}

// TestLogsHandler_EmptySid 验证 sid 缺失返回 400。
func TestLogsHandler_EmptySid(t *testing.T) {
	rc := serveLogs(newLogsHandlerForTest(t.TempDir()), "", false)

	if got := rc.Response.StatusCode(); got != 400 {
		t.Fatalf("expected 400, got %d body=%s", got, rc.Response.Body())
	}
	body := string(rc.Response.Body())
	if !contains(body, `"invalid_request"`) || !contains(body, "session_id 参数缺失") {
		t.Errorf("400 响应体不符合契约: %s", body)
	}
}

// TestLogsHandler_NoLogs 验证会话无日志时返回 200，logs 为 [] 而非 null。
func TestLogsHandler_NoLogs(t *testing.T) {
	rc := serveLogs(newLogsHandlerForTest(t.TempDir()), "sess_none", true)
	resp := decodeLogs(t, rc)

	if resp.Count != 0 {
		t.Errorf("count = %d, want 0", resp.Count)
	}
	if resp.Truncated {
		t.Errorf("truncated = true, want false")
	}
	body := string(rc.Response.Body())
	if !contains(body, `"logs":[]`) {
		t.Errorf("expected logs:[] in body, got %s", body)
	}
	if contains(body, `"logs":null`) {
		t.Errorf("logs 不允许为 null: %s", body)
	}
}

// TestLogsHandler_Truncated 验证超过 1000 条时截断并保留最新的 1000 条。
func TestLogsHandler_Truncated(t *testing.T) {
	dir := t.TempDir()
	lines := make([]string, 0, 1001)
	for i := 1; i <= 1001; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"timestamp":"2026-09-05T10:00:00","level":"info","message":"msg-%04d","caller":"a.go:1","session_id":"sess_big"}`, i))
	}
	writeTodayLogFile(t, dir, lines)

	resp := decodeLogs(t, serveLogs(newLogsHandlerForTest(dir), "sess_big", true))

	if !resp.Truncated {
		t.Errorf("truncated = false, want true")
	}
	if resp.Count != 1000 || len(resp.Logs) != 1000 {
		t.Fatalf("count = %d, len(logs) = %d, want 1000/1000", resp.Count, len(resp.Logs))
	}
	// 保留最新的 1000 条：第一条应是第 2 条写入的内容
	if resp.Logs[0].Message != "msg-0002" {
		t.Errorf("logs[0].message = %s, want msg-0002", resp.Logs[0].Message)
	}
	if resp.Logs[999].Message != "msg-1001" {
		t.Errorf("logs[999].message = %s, want msg-1001", resp.Logs[999].Message)
	}
}
