package agent

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
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
	"github.com/zfd81/groot/internal/setting"
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
		setting.New(config.Bootstrap{}, nil),
		log,
	)

	task := &Task{
		ID:          "chat_exec_001",
		Round:       1,
		Instruction: "ping",
		AgentName:   "not-exist-agent", // 非主 Agent -> 进入 Solo 分支
		StartTime:   time.Now(),
	}

	e.Execute(context.Background(), sessionID, task, NopEventWriter())

	if task.Status != StatusFailed {
		t.Errorf("soloErr 路径应将任务标记为 failed，实际 %v", task.Status)
	}
	if task.Error == nil || task.Error.Code != "subagent_unavailable" {
		t.Errorf("soloErr 路径应回写 subagent_unavailable 错误，实际 %+v", task.Error)
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

// TestExecutor_NilEventWriterDoesNotPanic Execute 的第四参数传 nil 时按 NopEventWriter 处理，
// 不会因空接口调用而 panic，任务状态照常回写。
func TestExecutor_NilEventWriterDoesNotPanic(t *testing.T) {
	const sessionID = "sess_exec_nil"

	e := NewExecutor(
		t.TempDir(),
		newRealMemoryManager(t),
		nil,
		nil,
		nil, // subAgentRegistry 为 nil -> soloErr，提前返回
		nil,
		nil,
		setting.New(config.Bootstrap{}, nil),
		logger.NewNop(),
	)

	task := &Task{
		ID:          "chat_exec_nil",
		Round:       1,
		Instruction: "ping",
		AgentName:   "not-exist-agent", // 非主 Agent -> 进入 Solo 分支
		StartTime:   time.Now(),
	}

	// 传字面 nil：Execute 内部必须替换为 NopEventWriter() 后再写出事件。
	e.Execute(context.Background(), sessionID, task, nil)

	if task.Status != StatusFailed {
		t.Errorf("soloErr 路径应将任务标记为 failed，实际 %v", task.Status)
	}
	if task.Error == nil || task.Error.Code != "subagent_unavailable" {
		t.Errorf("soloErr 路径应回写 subagent_unavailable 错误，实际 %+v", task.Error)
	}
}

// TestExecute_PassesHistoryForToEngineRun 用 AST 锁住 Execute 内对 engine.Run 的调用接线：
// 第 5 个实参（索引 4）必须是 historyFor(...) 调用。
// 直接调 historyFor 的测试只锁住 helper 实现，锁不住调用点，本测试补上这一环。
func TestExecute_PassesHistoryForToEngineRun(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "executor.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 executor.go 失败: %v", err)
	}

	// 1. 找到 func (e *Executor) Execute 声明
	var executeDecl *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name == nil || fn.Name.Name != "Execute" {
			continue
		}
		recv := fn.Recv.List
		if len(recv) != 1 {
			continue
		}
		star, ok := recv[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || ident.Name != "Executor" {
			continue
		}
		executeDecl = fn
		break
	}
	if executeDecl == nil {
		t.Fatal("executor.go 中未找到 func (e *Executor) Execute 声明")
	}

	// 2. 在 Execute 体内找到对 engine.Run( 的调用表达式
	var runCall *ast.CallExpr
	ast.Inspect(executeDecl.Body, func(n ast.Node) bool {
		if runCall != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "Run" {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || x.Name != "engine" {
			return true
		}
		runCall = call
		return false
	})
	if runCall == nil {
		t.Fatal("Execute 中未找到对 engine.Run 的调用")
	}

	// 3. 断言第 5 个实参是 historyFor(...) 调用
	if len(runCall.Args) < 5 {
		t.Fatalf("engine.Run 实参个数 = %d，want >= 5", len(runCall.Args))
	}
	arg := runCall.Args[4]
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		t.Fatalf("engine.Run 第 5 个实参不是 historyFor(...) 调用，实际是 %T", arg)
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "historyFor" {
		t.Fatalf("engine.Run 第 5 个实参不是 historyFor(...) 调用，Fun 实际是 %T", call.Fun)
	}
}

// TestSSEAgentName 主 Agent 名映射为空串（SSE 事件不带 agent_name），其余原样返回。
func TestSSEAgentName(t *testing.T) {
	cases := []struct{ in, want string }{
		{MainAgentName, ""},
		{"coder", "coder"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sseAgentName(c.in); got != c.want {
			t.Errorf("sseAgentName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
