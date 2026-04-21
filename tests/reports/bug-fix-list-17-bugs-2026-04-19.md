# Groot 程序Bug修复清单（17个确认Bug）

**生成日期:** 2026-04-19  
**用途:** 供程序员修复程序Bug使用  

---

## 📋 Bug清单总览

| 类别 | Bug数量 | 优先级 |
|------|---------|--------|
| Cancel状态处理 | 5 | 🔴 最高 |
| 并发冲突检查 | 2 | 🟠 高 |
| 附件类型验证 | 2 | 🟠 高 |
| Runtime State返回 | 2 | 🟡 中 |
| 功能缺失 | 4 | 🟡 中 |
| 其他逻辑错误 | 2 | 🟡 中 |

---

## 🔴 一、Cancel状态处理Bug（5个）- 最高优先级

---

### Bug #1: 取消后Memory保存状态错误

**测试名称:** `test_status_cancelled`  
**文件位置:** `tests/test_memory.py:435`

**错误现象:**
```
AssertionError: assert 'completed' == 'cancelled'
  - cancelled     ← 期望值：用户取消
  + completed     ← 实际值：正常完成
```

**问题描述:**  
用户取消对话后，Memory中保存的chat状态应该是 `cancelled`，但实际保存为 `completed`。

**需要修改的文件:**
- `internal/agent/executor.go`
- `internal/memory/manager.go`

**修复方案:**
```go
// executor.go - 取消处理逻辑
func (e *Executor) Execute(ctx context.Context, req *Request) (*Result, error) {
    // 监听取消信号
    select {
    case <-ctx.Done():
        // ===== Bug修复：取消时设置正确状态 =====
        record := &ChatRecord{
            ChatID:    e.chatID,
            SessionID: req.SessionID,
            Status:    "cancelled",  // ← 必须是 cancelled
            EndedAt:   time.Now(),
        }
        e.memory.SaveChatRecord(record)
        e.sse.WriteCompleted("cancelled", e.elapsedTime(), nil, nil)
        return &Result{Status: "cancelled"}, nil
        
    case result := <-done:
        // 正常完成
        record := &ChatRecord{
            Status: "completed",  // ← 正常完成才是 completed
        }
        e.memory.SaveChatRecord(record)
        return result, nil
    }
}

// memory/manager.go - 不要覆盖已设置的状态
func SaveChatRecord(record *ChatRecord) error {
    // ===== Bug修复：保留传入的状态值 =====
    // 删除以下代码：
    // if record.Status == "" {
    //     record.Status = "completed"
    // }
    
    return saveToFile(record)
}
```

---

### Bug #2: SSE Completed事件取消状态错误

**测试名称:** `test_cancelled_completed_event`  
**文件位置:** `tests/test_sse_events.py:338`

**错误现象:**
```
AssertionError: assert 'success' == 'cancelled'
  - cancelled     ← 期望值
  + success       ← 实际值
```

**问题描述:**  
用户取消对话后，SSE的completed事件应该包含 `status="cancelled"`，但实际返回 `success`。

**需要修改的文件:**
- `internal/agent/sse.go`
- `internal/agent/executor.go`

**修复方案:**
```go
// sse.go - WriteCompleted函数
func (s *SSEWriter) WriteCompleted(status string, duration int, result interface{}, err error) {
    event := SSEEvent{
        Event: "completed",
        Data: map[string]interface{}{
            "status":    status,     // ← 确保 status 参数正确传递
            "duration":  duration,
            "result":    result,
            "error":     err,
            "timestamp": time.Now().Unix(),
        },
    }
    s.WriteEvent(event)
}

// executor.go - 调用时传入正确状态
// 取消时：
sse.WriteCompleted("cancelled", duration, nil, nil)  // ← 传 "cancelled"

// 正常完成时：
sse.WriteCompleted("completed", duration, result, nil)
```

---

### Bug #3: 取消中断LLM调用状态错误

**测试名称:** `test_cancel_interrupts_llm_call`  
**文件位置:** `tests/test_supplementary.py:815`

**错误现象:**
```
AssertionError: assert 'success' == 'cancelled'
```

**问题描述:**  
同Bug #1、#2，取消时状态处理错误。

**需要修改的文件:**  
同Bug #1、#2

---

### Bug #4: 取消SSE推送事件状态错误

**测试名称:** `test_cancel_sse_pushes_event`  
**文件位置:** `tests/test_supplementary.py:865`

**错误现象:**
```
AssertionError: assert 'success' == 'cancelled'
```

**问题描述:**  
同Bug #2，SSE completed事件状态错误。

**需要修改的文件:**  
同Bug #2

---

### Bug #5: 取消无运行对话返回错误状态

**测试名称:** `test_cancel_no_running_chat`  
**文件位置:** `tests/test_api_endpoints.py:314`

**错误现象:**
```
AssertionError: assert 'success' == 'no_running_chat'
  - no_running_chat     ← 期望值：提示没有运行中的对话
  + success             ← 实际值：操作成功
```

**问题描述:**  
当没有运行中的对话时调用取消接口（DELETE /chat），应该返回特定状态 `no_running_chat` 提示用户，但实际返回 `success`，会让用户误以为取消成功。

**需要修改的文件:**
- `internal/api/handlers/chat.go` - HandleCancelChat函数

**修复方案:**
```go
func HandleCancelChat(w http.ResponseWriter, r *http.Request) {
    sessionID := getSessionIDFromRequest(r)
    
    // ===== Bug修复：检查是否有运行中的对话 =====
    activeChat := runtimeState.GetActiveChat(sessionID)
    if activeChat == nil {
        // 没有运行中的对话
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(200)
        json.NewEncoder(w).Encode(map[string]interface{}{
            "status":  "no_running_chat",  // ← 返回这个状态
            "message": "No active chat to cancel",
            "session_id": sessionID,
        })
        return
    }
    
    // 有运行中的对话，执行取消
    runtimeState.Cancel(sessionID)
    activeChat.Cancel()
    
    // 返回取消成功
    json.NewEncoder(w).Encode(map[string]interface{}{
        "status":  "success",
        "message": "Chat cancelled successfully",
        "chat_id": activeChat.ChatID,
    })
}
```

---

## 🟠 二、并发冲突检查Bug（2个）- 高优先级

---

### Bug #6: 同Session并发请求未返回409

**测试名称:** `test_concurrent_session_conflict`  
**文件位置:** `tests/test_api_endpoints.py:240`

**错误现象:**
```
assert response2.status_code == 409
E   assert 200 == 409
```

**问题描述:**  
同一个session正在执行对话时，第二个并发请求应该返回 `409 Conflict`，但实际返回 `200` 成功接受。这会导致同一session同时执行多个对话，可能造成资源竞争和数据混乱。

**需要修改的文件:**
- `internal/api/handlers/chat.go` - HandleChat函数
- `internal/runtime/state.go` - IsRunning方法

**修复方案:**
```go
// handlers/chat.go - HandleChat函数开头添加并发检查
func HandleChat(w http.ResponseWriter, r *http.Request) {
    sessionID := getSessionIDFromRequest(r)
    
    // ===== Bug修复：检查并发冲突 =====
    if runtimeState.IsRunning(sessionID) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(409)
        json.NewEncoder(w).Encode(map[string]interface{}{
            "status":  "session_conflict",
            "message": "Session is already running a chat",
            "code":    409,
            "session_id": sessionID,
        })
        return
    }
    
    // 注册活跃对话
    chatID := generateChatID()
    runtimeState.Register(sessionID, chatID)
    
    // 继续正常处理...
}

// runtime/state.go - 确保 IsRunning 方法正确实现
func (s *State) IsRunning(sessionID string) bool {
    s.mu.RLock()
    defer s.mu.RUnlock()
    _, exists := s.activeChats[sessionID]
    return exists
}
```

---

### Bug #7: 409错误格式测试

**测试名称:** `test_409_error_format`  
**文件位置:** `tests/test_errors.py:70`

**错误现象:**
```
assert response2.status_code == 409
E   assert 200 == 409
```

**问题描述:**  
同Bug #6，并发请求未返回409。

**需要修改的文件:**  
同Bug #6

---

## 🟠 三、附件类型验证Bug（2个）- 高优先级

---

### Bug #8: 不允许的附件类型返回200而非400

**测试名称:** `test_attachment_type_not_allowed`  
**文件位置:** `tests/test_attachments.py:154`

**错误现象:**
```
assert response.status_code == 400
E   assert 200 == 400
```

**问题描述:**  
发送不在白名单中的附件类型（如 `"invalid_type"`），应该返回 `400` 错误拒绝，但实际返回 `200` 成功接受。这会导致不安全的附件类型被上传。

**需要修改的文件:**
- `internal/attachment/validator.go`

**修复方案:**
```go
// validator.go - 加强类型验证
func ValidateAttachmentType(attType string) error {
    allowedTypes := config.GetAllowedAttachmentTypes()
    
    // ===== Bug修复：确保验证生效 =====
    for _, allowed := range allowedTypes {
        if attType == allowed {
            return nil  // 类型允许
        }
    }
    
    // 类型不允许，返回错误
    return &ValidationError{
        HTTPCode: 400,
        Code:     "attachment_type_not_allowed",
        Message:  fmt.Sprintf("Attachment type '%s' is not allowed. Allowed types: %v", attType, allowedTypes),
    }
}

// attachment/handler.go - 确保调用验证并返回错误
func HandleAttachments(attachments []Attachment) error {
    for _, att := range attachments {
        if err := ValidateAttachmentType(att.Type); err != nil {
            return err  // ← 返回错误，而不是忽略
        }
    }
    return nil
}
```

---

### Bug #9: 错误码列表测试

**测试名称:** `test_attachment_type_not_allowed` (errors模块)  
**文件位置:** `tests/test_errors.py:126`

**错误现象:**
```
assert response.status_code == 400
E   assert 200 == 400
```

**问题描述:**  
同Bug #8，类型验证失效。

**需要修改的文件:**  
同Bug #8

---

## 🟡 四、Runtime State返回值Bug（2个）- 中优先级

---

### Bug #10: GetActiveChat返回None

**测试名称:** `test_register_active_chat`  
**文件位置:** `tests/test_runtime_state.py:39`

**错误现象:**
```
assert data["chat"] is not None
E   assert None is not None
```

**问题描述:**  
注册活跃对话后，API返回的 `chat` 字段为 `None`，导致无法获取活跃对话信息。

**需要修改的文件:**
- `internal/runtime/state.go`
- `internal/api/handlers/status.go`

**修复方案:**
```go
// runtime/state.go - 确保 GetActiveChat 正确返回
func (s *State) GetActiveChat(sessionID string) *ActiveChat {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    chat, exists := s.activeChats[sessionID]
    if !exists {
        return nil  // 明确返回 nil
    }
    // ===== Bug修复：确保返回正确的结构 =====
    return chat
}

// handlers/status.go - 返回正确的响应结构
func HandleGetStatus(w http.ResponseWriter, r *http.Request) {
    sessionID := getSessionID(r)
    chat := runtimeState.GetActiveChat(sessionID)
    
    response := map[string]interface{}{}
    
    if chat != nil {
        response["status"] = "success"
        response["chat"] = map[string]interface{}{
            "chat_id":    chat.ChatID,
            "status":     "running",
            "started_at": chat.StartedAt,
            "instruction": chat.Instruction,
        }
    } else {
        response["status"] = "idle"
        response["chat"] = nil
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}
```

---

### Bug #11: IsRunning检查TypeError

**测试名称:** `test_is_running_check`  
**文件位置:** `tests/test_runtime_state.py:66`

**错误现象:**
```
TypeError: 'NoneType' object is not subscriptable
```

**问题描述:**  
同Bug #10，`chat` 返回 `None` 导致无法访问 `["status"]`。

**需要修改的文件:**  
同Bug #10

---

## 🟡 五、功能缺失Bug（4个）- 中优先级

---

### Bug #12: Chat记录缺少ended_at字段

**测试名称:** `test_get_chat_detail`  
**文件位置:** `tests/test_api_endpoints.py:403`

**错误现象:**
```
AssertionError: assert 'ended_at' in {'attachments': [], 'caller': '', 
    'chat_id': 'chat_20260419150256092', 'duration': 0, ...}
```

**问题描述:**  
Chat记录返回的字段中缺少 `ended_at`（结束时间）。

**需要修改的文件:**
- `internal/memory/types.go` - ChatRecord结构体
- `internal/memory/manager.go` - SaveChatRecord函数

**修复方案:**
```go
// types.go - 添加字段
type ChatRecord struct {
    ChatID      string    `json:"chat_id"`
    SessionID   string    `json:"session_id"`
    Instruction string    `json:"instruction"`
    StartedAt   time.Time `json:"started_at"`
    EndedAt     time.Time `json:"ended_at"`     // ← 添加这个字段
    Duration    int       `json:"duration"`
    Status      string    `json:"status"`
    Result      string    `json:"result,omitempty"`
    Error       string    `json:"error,omitempty"`
    Attachments []string  `json:"attachments"`
    Caller      string    `json:"caller"`
    // ...
}

// manager.go - 保存时设置结束时间
func SaveChatRecord(record *ChatRecord) error {
    if record.EndedAt.IsZero() {
        record.EndedAt = time.Now()  // ← 设置结束时间
    }
    // 保存...
}
```

---

### Bug #13: 附件处理没有发送SSE步骤事件

**测试名称:** `test_new_session_with_attachment`  
**文件位置:** `tests/test_api_endpoints.py:96`

**错误现象:**
```
assert len(file_read_steps) > 0
E   assert 0 > 0
```

**问题描述:**  
发送带附件的请求后，期望有 `step_start` 事件包含附件处理步骤，但实际返回空列表。附件处理过程中没有发送SSE步骤事件。

**需要修改的文件:**
- `internal/agent/executor.go` - 附件处理流程

**修复方案:**
```go
// executor.go - 附件处理流程添加SSE事件
func (e *Executor) processAttachments(ctx context.Context, attachments []Attachment) {
    for _, att := range attachments {
        stepID := generateStepID()
        
        // ===== Bug修复：发送 step_start 事件 =====
        e.sse.WriteEvent("step_start", map[string]interface{}{
            "step_id":       stepID,
            "step_type":     "file_read",
            "action":        "read_attachment",
            "filename":      att.Filename,
            "nesting_level": 0,
        })
        
        // 处理附件内容
        content, err := e.attachmentHandler.Process(ctx, att)
        
        // ===== Bug修复：发送 step_end 事件 =====
        e.sse.WriteEvent("step_end", map[string]interface{}{
            "step_id":       stepID,
            "step_type":     "file_read",
            "action":        "read_attachment",
            "output":        content,
            "error":         err,
            "nesting_level": 0,
        })
    }
}
```

---

### Bug #14: ReAct没有发送reasoning步骤事件

**测试名称:** `test_reasoning_step_emitted`  
**文件位置:** `tests/test_supplementary.py:890`

**错误现象:**
```
assert len(step_starts) > 0
E   assert 0 > 0
```

**问题描述:**  
ReAct执行过程中，应该发送 reasoning 步骤的 `step_start` 事件，但实际没有发送。

**需要修改的文件:**
- `internal/agent/executor.go` - ReAct执行逻辑

**修复方案:**
```go
// executor.go - ReAct执行流程添加SSE事件
func (e *Executor) executeReAct(ctx context.Context, req *Request) {
    // ===== Bug修复：发送 reasoning 步骤 =====
    stepID := generateStepID()
    
    // 发送 reasoning 的 step_start
    e.sse.WriteEvent("step_start", map[string]interface{}{
        "step_id":       stepID,
        "step_type":     "reasoning",
        "action":        "analyze_request",
        "input":         req.Instruction,
        "nesting_level": 0,
    })
    
    // 执行 reasoning（分析请求）
    reasoning := e.llm.Analyze(ctx, req.Instruction)
    
    // 发送 reasoning 的 step_end
    e.sse.WriteEvent("step_end", map[string]interface{}{
        "step_id":       stepID,
        "step_type":     "reasoning",
        "action":        "analyze_request",
        "output":        reasoning,
        "nesting_level": 0,
    })
    
    // 继续 acting, observing...
}
```

---

### Bug #15: URL类型附件不在白名单

**测试名称:** `test_url_attachment`  
**文件位置:** `tests/test_attachments.py:60`

**错误现象:**
```
assert response.status_code == 200
E   assert 400 == 200
```

**问题描述:**  
URL类型附件（type="url"）应该被接受处理，但返回400错误。URL类型不在附件类型白名单中。

**需要修改的文件:**
- `internal/attachment/validator.go` 或配置文件

**修复方案:**
```go
// validator.go - 添加 url 类型到白名单
var AllowedAttachmentTypes = []string{
    "base64",
    "url",      // ← 添加 url 类型
    "text",
    "file",
    "pdf",
    "doc",
    "docx",
    // ...
}

// 或者在配置文件 config.yaml 中：
attachment:
  allowed_types:
    - base64
    - url        # ← 添加 url 类型
    - text
    - file
    - pdf
```

---

## 🟡 六、其他逻辑错误Bug（2个）- 中优先级

---

### Bug #16: 错误情况下返回空响应

**测试名称:** `test_error_contains_session_id_when_relevant`  
**文件位置:** `tests/test_errors.py:311`

**错误现象:**
```
requests.exceptions.JSONDecodeError: Expecting value: line 1 column 1 (char 0)
```

**问题描述:**  
某些错误情况下，API返回空响应而非JSON格式，导致无法解析。

**需要修改的文件:**
- `internal/api/handlers/*.go` - 确保所有情况下返回JSON

**修复方案:**
```go
// 所有 handler 中确保返回有效 JSON
func HandleError(w http.ResponseWriter, err error) {
    w.Header().Set("Content-Type", "application/json")
    
    // ===== Bug修复：确保返回有效 JSON =====
    json.NewEncoder(w).Encode(map[string]interface{}{
        "status":  "error",
        "message": err.Message,
        "code":    err.HTTPCode,
        "session_id": getSessionIDFromContext(),  // ← 包含 session_id
    })
}
```

---

### Bug #17: 清理后活跃session状态丢失

**测试名称:** `test_cleanup_preserves_active_sessions`  
**文件位置:** `tests/test_supplementary.py:592`

**错误现象:**
```
assert status.json()["chat"] is not None
E   assert None is not None
```

**问题描述:**  
同Bug #10，GetActiveChat返回None导致测试失败。

**需要修改的文件:**  
同Bug #10

---

## 📊 按文件归类的修复清单

| 文件路径 | Bug编号 | 修复内容 |
|---------|---------|---------|
| `internal/agent/executor.go` | #1, #2, #3, #4, #13, #14 | Cancel状态 + SSE步骤事件 |
| `internal/agent/sse.go` | #2, #3, #4 | WriteCompleted状态传递 |
| `internal/memory/manager.go` | #1 | SaveChatRecord状态保留 |
| `internal/memory/types.go` | #12 | ChatRecord添加ended_at字段 |
| `internal/api/handlers/chat.go` | #5, #6, #7 | Cancel检查 + 并发检查 |
| `internal/api/handlers/status.go` | #10, #11, #17 | GetActiveChat返回结构 |
| `internal/runtime/state.go` | #6, #10, #11, #17 | IsRunning + GetActiveChat实现 |
| `internal/attachment/validator.go` | #8, #9, #15 | 类型验证 + url白名单 |

---

## 🎯 修复优先级建议

### 第一阶段（最高优先级）- Cancel状态

**修复Bug:** #1, #2, #3, #4, #5  
**修改文件:** `executor.go`, `sse.go`, `memory.go`, `handlers/chat.go`  
**预计工作量:** 2小时

---

### 第二阶段（高优先级）- 并发 + 附件验证

**修复Bug:** #6, #7, #8, #9  
**修改文件:** `handlers/chat.go`, `runtime/state.go`, `attachment/validator.go`  
**预计工作量:** 1小时

---

### 第三阶段（中优先级）- 功能完善

**修复Bug:** #10, #11, #12, #13, #14, #15, #16, #17  
**修改文件:** `runtime/state.go`, `handlers/status.go`, `memory/types.go`, `executor.go`  
**预计工作量:** 2小时

---

## ✅ 修复完成后的预期通过率

| 阶段 | 预期通过率 |
|------|-----------|
| 第一阶段完成后 | **89.6%** (240/270) |
| 第二阶段完成后 | **91.1%** (246/270) |
| 第三阶段完成后 | **93.3%** (252/270) |

---

**报告生成:** Claude Code  
**报告日期:** 2026-04-19  
**用途:** 程序员Bug修复参考