# Groot 深度测试报告

**日期:** 2026-04-19  
**LLM:** Qwen3.5-122B-A10B-6bit (http://127.0.0.1:8230)

---

## 测试结果统计

### 第一轮测试（无真实LLM）

| 指标 | 数值 |
|------|------|
| 总测试数 | 252 |
| 通过 | 159 |
| 失败 | 85 |
| 跳过 | 8 |
| 通过率 | 63.1% |

### 第二轮测试（真实LLM）

| 指标 | 数值 |
|------|------|
| 新增测试数 | 18 |
| 通过 | 3 |
| 失败 | 15 |
| 通过率 | 16.7% |

---

## 发现的关键问题

### 问题1: SSE 连接过早关闭 (P0 - 紧急)

**问题描述:**  
Agent 执行使用 goroutine 异步运行，HTTP handler 在启动 goroutine 后立即返回，导致 SSE 连接关闭，后续事件无法发送。

**位置:** `internal/api/handler/chat.go:215`

```go
// 当前实现
go h.agentExecutor.Execute(sessionID, task, sseWriter, activeChat.CancelCh)
// handler 立即返回，连接关闭

// 正确实现应该是同步执行，或使用机制等待完成
h.agentExecutor.Execute(sessionID, task, sseWriter, activeChat.CancelCh)
// 或者使用 channel 等待完成信号
```

**影响:**  
- 所有涉及 LLM 交互的测试失败
- 用户无法收到完整的对话响应
- SSE 只发送 intent 事件，没有后续事件

---

### 问题2: intent 事件重复发送

**问题描述:**  
从客户端观察，intent 事件被发送了两次。

**日志输出:**
```
event: intent
data: {"timestamp":"2026-04-19T00:56:25Z"}

event: intent
data: {"timestamp":"2026-04-19T00:56:25Z"}
```

**可能原因:**  
- 需要检查是否有多个地方调用 WriteIntent()
- 或 SSE Writer 实现有bug

---

### 问题3: 字段命名不一致

| 设计文档定义 | 实际实现 |
|-------------|---------|
| `chats_running` | `tasks_running` |
| `memory` in health checks | 未包含 |
| `result_attachments` | 可能缺失 |

---

### 问题4: Health 接口缺少 memory 检查

**设计文档要求:**
```json
{
  "checks": {
    "memory": {"status": "healthy", "used_mb": 256}
  }
}
```

**实际返回:** 无 memory 字段

---

### 问题5: 认证默认禁用

测试期望认证启用，但默认配置 `security.auth.enabled: false`

---

## 测试通过的模块

1. **LLM 连接测试** - Qwen 模型正常响应 ✓
2. **Health 基础检查** - 服务存活检查 ✓
3. **Skills/MCP 管理器** - 工具列表正常 ✓
4. **Memory 目录创建** - 会话目录正常创建 ✓
5. **RuntimeState 注册** - ActiveChat 注册正常 ✓

---

## LLM 验证测试

### 直接 LLM API 测试

```bash
curl -X POST http://127.0.0.1:8230/v1/chat/completions \
  -H "Authorization: Bearer bonc1q2w3e" \
  -d '{"model": "Qwen3.5-122B-A10B-6bit", "messages": [{"role": "user", "content": "你好"}]}'
```

**结果:** ✓ 成功返回响应
```json
{"content": "你好！有什么我可以帮你的吗？"}
```

---

## 修复建议优先级

### P0 - 必须立即修复

1. **修复 SSE goroutine 问题**
   - 将异步执行改为同步执行
   - 或使用 sync.WaitGroup/channel 等待完成
   - 确保 SSE 连接保持打开直到 completed 事件发送

2. **修复 intent 重复发送**
   - 检查 SSE Writer 实现
   - 确保 intent 只发送一次

### P1 - 重要修复

1. **统一字段命名**
   - 使用设计文档定义的字段名
   - `tasks_running` → `chats_running`

2. **添加 memory 健康检查**
   - 在 health 接口添加 memory 状态

3. **确保 completed 包含所有字段**
   - round, duration, result 等

### P2 - 可延后修复

1. **调整认证默认配置**
2. **完善热插拔测试等待时间**

---

## 修复代码示例

### SSE 连接问题修复

```go
// chat.go - 方案1: 同步执行
func (h *ChatHandler) Handle(ctx context.Context, rc *app.RequestContext) {
    // ... 设置 SSE headers ...
    
    // 直接同步执行（不使用 goroutine）
    h.agentExecutor.Execute(sessionID, task, sseWriter, activeChat.CancelCh)
    
    // 执行完成后 handler 自然返回
}

// 或者方案2: 使用 channel 等待
doneCh := make(chan struct{})
go func() {
    h.agentExecutor.Execute(sessionID, task, sseWriter, activeChat.CancelCh)
    close(doneCh)
}()
<-doneCh  // 等待执行完成
```

---

## 结论

核心问题在于 **SSE goroutine 异步执行导致连接过早关闭**。修复此问题后，预计大部分测试将通过。

建议优先修复：
1. SSE 同步执行问题
2. intent 重复发送问题
3. 字段命名统一

修复后预期通过率: **90%+**