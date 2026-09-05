package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
)

// writeLogFile 在 dir 下按 pattern 写一个指定日期的日志文件
func writeLogFile(t *testing.T, dir, date, content string) {
	t.Helper()
	name := "groot-" + date + ".log"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("写测试日志文件失败: %v", err)
	}
}

func testFileCfg(dir string) config.LogFileConfig {
	return config.LogFileConfig{Directory: dir, FilenamePattern: "groot-{date}.log"}
}

func TestReadSessionLogs_MatchAndSkip(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	writeLogFile(t, dir, today,
		`{"timestamp":"t1","level":"info","message":"m1","caller":"a.go:1","session_id":"sess_a"}`+"\n"+
			`{"timestamp":"t2","level":"error","message":"m2","caller":"a.go:2","session_id":"sess_b"}`+"\n"+
			`不是JSON的坏行`+"\n"+
			`{"timestamp":"t3","level":"warn","message":"m3","caller":"a.go:3","session_id":"sess_a","tool":"web_search"}`+"\n"+
			`{"timestamp":"t4","level":"info","message":"无会话日志"}`+"\n")

	logs, truncated := ReadSessionLogs(testFileCfg(dir), "sess_a", 7, 1000)
	if truncated {
		t.Error("不应截断")
	}
	if len(logs) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条", len(logs))
	}
	if logs[0].Message != "m1" || logs[1].Message != "m3" {
		t.Errorf("应按文件顺序返回 m1、m3，实际 %q、%q", logs[0].Message, logs[1].Message)
	}
	if logs[1].Level != "warn" || logs[1].Caller != "a.go:3" {
		t.Errorf("字段解析错误: %+v", logs[1])
	}
	if v, ok := logs[1].Fields["tool"]; !ok || v != "web_search" {
		t.Errorf("非标准字段应进入 Fields: %+v", logs[1].Fields)
	}
	if logs[0].Fields != nil {
		t.Errorf("无额外字段时 Fields 应为 nil: %+v", logs[0].Fields)
	}
}

func TestReadSessionLogs_MultiDayOrderAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	// 前天的文件缺失：应被跳过而不报错
	writeLogFile(t, dir, yesterday,
		`{"timestamp":"t1","level":"info","message":"昨天","session_id":"s1"}`+"\n")
	writeLogFile(t, dir, today,
		`{"timestamp":"t2","level":"info","message":"今天","session_id":"s1"}`+"\n")

	logs, _ := ReadSessionLogs(testFileCfg(dir), "s1", 7, 1000)
	if len(logs) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条", len(logs))
	}
	if logs[0].Message != "昨天" || logs[1].Message != "今天" {
		t.Errorf("应按日期从旧到新排列: %q, %q", logs[0].Message, logs[1].Message)
	}
}

func TestReadSessionLogs_Truncate(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	content := ""
	for _, m := range []string{"m1", "m2", "m3", "m4", "m5"} {
		content += `{"timestamp":"t","level":"info","message":"` + m + `","session_id":"s1"}` + "\n"
	}
	writeLogFile(t, dir, today, content)

	logs, truncated := ReadSessionLogs(testFileCfg(dir), "s1", 7, 3)
	if !truncated {
		t.Error("超过 limit 应标记 truncated")
	}
	if len(logs) != 3 {
		t.Fatalf("期望 3 条，实际 %d 条", len(logs))
	}
	if logs[0].Message != "m3" || logs[2].Message != "m5" {
		t.Errorf("应保留最新的 3 条 m3..m5，实际 %q..%q", logs[0].Message, logs[2].Message)
	}
}

func TestReadSessionLogs_OversizedLineSkipped(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	// 第 2 行超过 1MB：只应丢弃该行本身，不得中断文件后续行的扫描
	writeLogFile(t, dir, today,
		`{"timestamp":"t1","level":"info","message":"m1","session_id":"s1"}`+"\n"+
			strings.Repeat("x", 2<<20)+"\n"+
			`{"timestamp":"t3","level":"info","message":"m3","session_id":"s1"}`+"\n")

	logs, truncated := ReadSessionLogs(testFileCfg(dir), "s1", 7, 1000)
	if truncated {
		t.Error("不应截断")
	}
	if len(logs) != 2 {
		t.Fatalf("超长行只应丢弃自身，期望 2 条，实际 %d 条", len(logs))
	}
	if logs[0].Message != "m1" || logs[1].Message != "m3" {
		t.Errorf("期望 m1、m3，实际 %q、%q", logs[0].Message, logs[1].Message)
	}
}

func TestReadSessionLogs_EmptyCases(t *testing.T) {
	// 目录为空、会话不存在、sessionID 为空，均返回空且不报错
	dir := t.TempDir()
	if logs, _ := ReadSessionLogs(testFileCfg(dir), "nope", 7, 1000); len(logs) != 0 {
		t.Errorf("空目录应返回空列表")
	}
	if logs, _ := ReadSessionLogs(testFileCfg(dir), "", 7, 1000); len(logs) != 0 {
		t.Errorf("空 sessionID 应返回空列表")
	}
	if logs, _ := ReadSessionLogs(config.LogFileConfig{}, "s1", 7, 1000); len(logs) != 0 {
		t.Errorf("空目录配置应返回空列表")
	}
}
