package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo/memorydb"
)

// newExecutorFileLogger 创建写入 dir 的 JSON 文件 logger，
// 便于测试读回真实日志行断言字段。
func newExecutorFileLogger(t *testing.T, dir string) *logger.Logger {
	t.Helper()
	return logger.New(config.LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: []string{"file"},
		File: config.LogFileConfig{
			Directory:       dir,
			FilenamePattern: "test-{date}.log",
		},
	})
}

// readExecutorLogLines 读取 dir 下唯一日志文件的非空行。
// 用 glob 匹配而非自行推算日期，避免跨午夜时文件名不一致。
func readExecutorLogLines(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "test-*.log"))
	if err != nil {
		t.Fatalf("查找日志文件失败: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("期望 1 个日志文件，实际 %d 个: %v", len(matches), matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// newRealMemoryManager 基于真实 sqlite 仓储构造 memory.Manager。
// 不预先创建 session，因此 SaveChatRecord 会返回 ErrNotFound，
// 天然触发 Execute 内的错误日志，无需任何 mock。
func newRealMemoryManager(t *testing.T) *memory.Manager {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return memory.NewManager(logger.NewNop(), memorydb.New(sqlxDB, dialect))
}

// TestExecutor_ExecuteLogsCarrySessionID 验证 Execute 产出的日志携带 session_id。
// 走 Solo 校验失败的提前返回路径（子 Agent 注册表未初始化），
// 不需要真实 LLM 即可触发 Execute 内的日志输出。
func TestExecutor_ExecuteLogsCarrySessionID(t *testing.T) {
	logDir := t.TempDir()
	log := newExecutorFileLogger(t, logDir)

	const sessionID = "sess_exec_001"

	e := NewExecutor(
		t.TempDir(),
		newRealMemoryManager(t),
		nil,
		nil,
		nil, // subAgentRegistry 为 nil -> soloErr，提前返回
		nil,
		nil,
		config.Config{},
		log,
	)

	buf := &bufFlusher{}
	sse := NewSSEWriter(buf, sessionID, "chat_exec_001", 1)

	task := &Task{
		ID:          "chat_exec_001",
		Round:       1,
		Instruction: "ping",
		AgentName:   "not-exist-agent", // 非主 Agent -> 进入 Solo 分支
		StartTime:   time.Now(),
	}

	e.Execute(context.Background(), sessionID, task, sse)

	if task.Status != StatusFailed {
		t.Errorf("soloErr 路径应将任务标记为 failed，实际 %v", task.Status)
	}

	lines := readExecutorLogLines(t, logDir)
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("Execute 未产出任何日志，无法验证 session_id 注入")
	}

	want := `"session_id":"` + sessionID + `"`
	for i, line := range lines {
		if !strings.Contains(line, want) {
			t.Errorf("第 %d 行日志缺少 %s: %s", i+1, want, line)
		}
	}

	// 明确断言错误日志确实来自 Execute 内的持久化失败分支
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "保存对话记录失败") {
		t.Errorf("期望捕获到「保存对话记录失败」日志，实际: %s", joined)
	}
}
