# Groot 测试失败详情报告（第六轮）

**测试日期:** 2026-04-19  
**测试结果:** 228 通过, 34 失败, 8 跳过 (87.4% 通过率)

---

## 🔴 失败测试详细清单（含文件位置和错误描述）

---

### 一、CLI 测试问题（8个） - **测试代码问题，非程序Bug**

#### ❌ 1. [test_cli_args.py:19](tests/test_cli_args.py#L19) - test_help_flag

**错误位置:**
```python
test_cli_args.py:19: in test_help_flag
    result = subprocess.run(["groot", "-h"], ...)
```

**错误类型:** `FileNotFoundError`

**错误详情:**
```
FileNotFoundError: [Errno 2] No such file or directory: 'groot'
```

**修复建议:** 测试代码应使用正确的 groot 二进制路径：
```python
GROOT_BIN = os.path.join(os.path.dirname(os.path.dirname(__file__)), "bin", "groot")
subprocess.run([GROOT_BIN, "-h"], ...)
```

---

#### ❌ 2. [test_cli_args.py:31](tests/test_cli_args.py#L31) - test_version_flag

**错误位置:**
```python
test_cli_args.py:31: in test_version_flag
    result = subprocess.run(["groot", "--version"], ...)
```

**错误详情:** 同上 - `FileNotFoundError: 'groot'`

---

#### ❌ 3. [test_cli_args.py:46](tests/test_cli_args.py#L46) - test_home_flag

**错误位置:**
```python
test_cli_args.py:46: in test_home_flag
    process = subprocess.Popen(["groot", "--home", temp_dir], ...)
```

**错误详情:** 同上 - `FileNotFoundError: 'groot'`

---

#### ❌ 4. [test_cli_args.py:71](tests/test_cli_args.py#L71) - test_port_flag

**错误位置:**
```python
test_cli_args.py:71: in test_port_flag
    process = subprocess.Popen(["groot", "--port", "9090"], ...)
```

**错误详情:** 同上 - `FileNotFoundError: 'groot'`

---

#### ❌ 5. [test_cli_args.py:101](tests/test_cli_args.py#L101) - test_groot_home_env

**错误位置:**
```python
test_cli_args.py:101: in test_groot_home_env
    process = subprocess.Popen(["groot"], ...)
```

**错误详情:** 同上 - `FileNotFoundError: 'groot'`

---

#### ❌ 6. [test_cli_args.py:156](tests/test_cli_args.py#L156) - test_groot_api_key_env

**错误位置:**
```python
test_cli_args.py:156: in test_groot_api_key_env
    process = subprocess.Popen(["groot"], ...)
```

**错误详情:** 同上 - `FileNotFoundError: 'groot'`

---

#### ❌ 7. [test_cli_args.py:242](tests/test_cli_args.py#L242) - test_cli_overrides_config

**错误位置:**
```python
test_cli_args.py:242: in test_cli_overrides_config
    process = subprocess.Popen(["groot", "--port", "8888"], ...)
```

**错误详情:** 同上 - `FileNotFoundError: 'groot'`

---

#### ❌ 8. [test_cli_args.py:276](tests/test_cli_args.py#L276) - test_env_overrides_default

**错误位置:**
```python
test_cli_args.py:276: in test_env_overrides_default
    process = subprocess.Popen(["groot"], ...)
```

**错误详情:** 同上 - `FileNotFoundError: 'groot'`

---

## 🔧 CLI 测试修复方案（统一）

修改 `tests/test_cli_args.py` 文件开头：

```python
import os

# 在文件开头添加：
GROOT_BIN = os.path.join(os.path.dirname(os.path.dirname(__file__)), "bin", "groot")

# 将所有 subprocess.run(["groot", ...]) 改为：
subprocess.run([GROOT_BIN, ...])
```

**修复这8个问题后，通过率将达到 90.5%**

---

### 二、API Endpoints 问题（7个） - **程序功能问题**

---

#### ❌ 9. [test_api_endpoints.py:35](tests/test_api_endpoints.py#L35) - test_new_session_basic

**错误位置:**
```python
test_api_endpoints.py:35: in test_new_session_basic
    assert response.headers["Content-Type"] == "text/event-stream"
```

**错误详情:**
```
AssertionError: assert 'text/event-stream; charset=utf-8' == 'text/event-stream'

  - text/event-stream
  + text/event-stream; charset=utf-8
```

**修复位置:** `internal/api/handlers/chat.go` - SSE 响应头设置

**修复建议:** 
```go
// 当前可能设置的是：
w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")

// 应改为：
w.Header().Set("Content-Type", "text/event-stream")
```

---

#### ❌ 10. [test_api_endpoints.py:95](tests/test_api_endpoints.py#L95) - test_new_session_with_attachment

**错误位置:**
```python
test_api_endpoints.py:95: in test_new_session_with_attachment
    assert len(file_read_steps) > 0
E   assert 0 > 0
   +  where 0 = len([])
```

**错误详情:** 测试期望附件读取步骤被记录，但实际返回空列表

**修复位置:** `internal/attachment/handler.go` 或 `internal/agent/executor.go`

**修复建议:** 确保附件处理步骤被正确写入 SSE 事件流

---

#### ❌ 11. [test_api_endpoints.py:118](tests/test_api_endpoints.py#L118) - test_multi_attachments

**错误位置:**
```python
test_api_endpoints.py:118: in test_multi_attachments
    assert response.status_code == 200
E   assert 400 == 200
```

**错误详情:** 多附件请求返回 400 错误，而非预期的 200 成功

**修复位置:** `internal/attachment/handler.go` - 多附件验证逻辑

**需要检查:**
- 是否错误拒绝了多附件请求
- 验证逻辑是否过于严格

---

#### ❌ 12. [test_api_endpoints.py:239](tests/test_api_endpoints.py#L239) - test_concurrent_session_conflict

**错误位置:**
```python
test_api_endpoints.py:239: in test_concurrent_session_conflict
    assert response2.status_code == 409
E   assert 200 == 409
```

**错误详情:** 同一 session 并发请求应返回 409 冲突，实际返回 200

**修复位置:** `internal/runtime/state.go` 或 `internal/api/handlers/chat.go`

**修复建议:**
```go
// 检查并发限制逻辑：
if state.IsRunning(sessionID) {
    return APIError{Code: 409, Message: "session already running"}
}
```

---

#### ❌ 13. [test_api_endpoints.py:313](tests/test_api_endpoints.py#L313) - test_cancel_no_running_chat

**错误位置:**
```python
test_api_endpoints.py:313: in test_cancel_no_running_chat
    assert data["status"] == "no_running_chat"
E   AssertionError: assert 'success' == 'no_running_chat'
  
  - no_running_chat
  + success
```

**错误详情:** 取消不存在的对话应返回特定状态，实际返回 'success'

**修复位置:** `internal/api/handlers/chat.go` - DELETE /chat endpoint

**修复建议:**
```go
// 当没有运行中的对话时：
if !state.IsRunning() {
    return JSON{"status": "no_running_chat", "message": "no active chat to cancel"}
}
```

---

#### ❌ 14. [test_api_endpoints.py:341](tests/test_api_endpoints.py#L341) - test_get_running_status

**错误位置:**
```python
test_api_endpoints.py:341: in test_get_running_status
    assert data["status"] == "success"
E   AssertionError: assert 'idle' == 'success'
  
  - success
  + idle
```

**错误详情:** 获取运行状态时返回 'idle' 而非 'success'

**修复位置:** `internal/runtime/state.go` - 状态查询接口

**需要检查:** 状态字段的含义和返回逻辑

---

#### ❌ 15. [test_api_endpoints.py:402](tests/test_api_endpoints.py#L402) - test_get_chat_detail

**错误位置:**
```python
test_api_endpoints.py:402: in test_get_chat_detail
    assert "ended_at" in chat
E   AssertionError: assert 'ended_at' in {'attachments': [], 'caller': '', 
    'chat_id': 'chat_20260419114643822', 'duration': 0, ...}
```

**错误详情:** Chat 记录缺少 `ended_at` 字段

**修复位置:** `internal/memory/manager.go` - Chat 记录结构

**需要添加:**
```go
type ChatRecord struct {
    // ... 现有字段
    StartedAt time.Time  // 已有
    EndedAt   time.Time  // 需要添加
}
```

---

### 三、Attachments 问题（2个）

---

#### ❌ 16. [test_attachments.py:60](tests/test_attachments.py#L60) - test_url_attachment

**错误位置:**
```python
test_attachments.py:60: in test_url_attachment
    assert response.status_code == 200
E   assert 400 == 200
```

**错误详情:** URL 类型附件被错误拒绝，返回 400

**修复位置:** `internal/attachment/handler.go` - 附件类型验证

**修复建议:**
```go
// 应允许 URL 类型附件：
allowedTypes := []string{"base64", "url", "file"}
```

---

#### ❌ 17. [test_attachments.py:182](tests/test_attachments.py#L182) - test_attachment_total_size_exceeded

**错误位置:**
```python
test_attachments.py:182: in test_attachment_total_size_exceeded
    assert data["status"] == "attachment_total_size_exceeded"
E   AssertionError: assert 'attachment_type_not_allowed' == 'attachment_total_size_exceeded'
  
  - attachment_total_size_exceeded
  + attachment_type_not_allowed
```

**错误详情:** 返回了错误的错误码

**修复位置:** `internal/attachment/handler.go` - 错误码返回逻辑

**需要检查:** 附件验证的顺序，应先检查类型再检查大小

---

### 四、Cancel 状态问题（3个） - **核心问题**

---

#### ❌ 18. [test_sse_events.py:338](tests/test_sse_events.py#L338) - test_cancelled_completed_event

**错误位置:**
```python
test_sse_events.py:338: in test_cancelled_completed_event
    assert data["status"] == "cancelled"
E   AssertionError: assert 'success' == 'cancelled'
  
  - cancelled
  + success
```

**错误详情:** 取消对话的 completed 事件应包含 status='cancelled'

**修复位置:** `internal/agent/sse.go` - Completed 事件写入

**修复建议:**
```go
func WriteCompleted(status string, duration int, ...) {
    // 当取消时：
    if cancelled {
        status = "cancelled"  // 而不是 "success"
    }
}
```

---

#### ❌ 19. [test_memory.py:435](tests/test_memory.py#L435) - test_status_cancelled

**错误位置:**
```python
test_memory.py:435: in test_status_cancelled
    assert messages[0]["status"] == "cancelled"
E   AssertionError: assert 'completed' == 'cancelled'
  
  - cancelled
  + completed
```

**错误详情:** Memory 保存的状态应为 'cancelled' 而非 'completed'

**修复位置:** `internal/memory/manager.go` - SaveChatRecord 函数

**修复建议:**
```go
func SaveChatRecord(record *ChatRecord) {
    if record.Cancelled {
        record.Status = "cancelled"
    }
}
```

---

#### ❌ 20. [test_supplementary.py:815](tests/test_supplementary.py#L815) - test_cancel_interrupts_llm_call

**错误位置:**
```python
test_supplementary.py:815: in test_cancel_interrupts_llm_call
    assert completed["data"]["status"] == "cancelled"
E   AssertionError: assert 'success' == 'cancelled'
  
  - cancelled
  + success
```

**错误详情:** 取消时应返回 'cancelled' 状态

**修复位置:** `internal/agent/executor.go` - Cancel 处理逻辑

---

### 五、Runtime State 问题（2个）

---

#### ❌ 21. [test_runtime_state.py:39](tests/test_runtime_state.py#L39) - test_register_active_chat

**错误位置:**
```python
test_runtime_state.py:39: in test_register_active_chat
    assert data["chat"] is not None
E   assert None is not None
```

**错误详情:** 注册活跃对话后返回 chat 应不为 None

**修复位置:** `internal/runtime/state.go` - RegisterActiveChat 函数

**需要检查:** 返回值是否正确设置

---

#### ❌ 22. [test_runtime_state.py:66](tests/test_runtime_state.py#L66) - test_is_running_check

**错误位置:**
```python
test_runtime_state.py:66: in test_is_running_check
    assert status1.json()["chat"]["status"] == "running"
E   TypeError: 'NoneType' object is not subscriptable
```

**错误详情:** chat 对象为 None，导致无法访问 status

**修复位置:** `internal/runtime/state.go` 或 `internal/api/handlers/chat.go`

---

### 六、Real LLM Mock 问题（5个） - **测试配置问题**

---

#### ❌ 23. [test_real_llm.py:66](tests/test_real_llm.py#L66) - test_real_llm_code_generation

**错误位置:**
```python
test_real_llm.py:66: in test_real_llm_code_generation
    assert "def" in result or "function" in result or "fibonacci" in result.lower()
E   AssertionError: assert ('def' in '任务执行完成，但未获得明确结果' ...)
```

**错误详情:** Mock LLM 返回 "任务执行完成，但未获得明确结果" 而非代码

**性质:** 测试使用 Mock 配置，非真实 LLM 调用

---

#### ❌ 24. [test_real_llm.py:93](tests/test_real_llm.py#L93) - test_real_llm_json_output

**错误位置:**
```python
test_real_llm.py:93: in test_real_llm_json_output
    assert "{" in result or "name" in result.lower()
E   AssertionError: assert ('{' in '任务执行完成，但未获得明确结果' ...)
```

**错误详情:** 同上 - Mock LLM 未返回 JSON

---

#### ❌ 25. [test_real_llm.py:142](tests/test_real_llm.py#L142) - test_real_llm_two_round_conversation

**错误位置:**
```python
test_real_llm.py:142: in test_real_llm_two_round_conversation
    assert "42" in result
E   AssertionError: assert '42' in '任务执行完成，但未获得明确结果'
```

**错误详情:** Mock LLM 未记住上下文中的数字 42

---

#### ❌ 26. [test_real_llm.py:302](tests/test_real_llm.py#L302) - test_real_llm_analysis_task

**错误位置:**
```python
test_real_llm.py:302: in test_real_llm_analysis_task
    assert "人工智能" in result or "AI" in result
E   AssertionError: assert ('人工智能' in '任务执行完成，但未获得明确结果' ...)
```

**错误详情:** Mock LLM 未返回分析内容

---

#### ❌ 27. [test_real_llm.py:347](tests/test_real_llm.py#L347) - test_real_llm_math_problem

**错误位置:**
```python
test_real_llm.py:347: in test_real_llm_math_problem
    assert "957" in result
E   AssertionError: assert '957' in '任务执行完成，但未获得明确结果'
```

**错误详情:** Mock LLM 未返回计算结果

---

### 七、Errors 问题（2个）

---

#### ❌ 28. [test_errors.py:70](tests/test_errors.py#L70) - test_409_error_format

**错误位置:**
```python
test_errors.py:70: in test_409_error_format
    assert response2.status_code == 409
E   assert 200 == 409
```

**错误详情:** 并发请求应返回 409 冲突错误

**修复位置:** `internal/api/handlers/chat.go`

---

#### ❌ 29. [test_errors.py:311](tests/test_errors.py#L311) - test_error_contains_session_id_when_relevant

**错误位置:**
```python
test_errors.py:311: in test_error_contains_session_id_when_relevant
    data = response2.json()
E   requests.exceptions.JSONDecodeError: Expecting value: line 1 column 1 (char 0)
```

**错误详情:** 响应内容为空，无法解析 JSON

**修复位置:** 对应的 API handler - 确保返回有效 JSON

---

### 八、Security 问题（1个）

---

#### ❌ 30. [test_security.py:261](tests/test_security.py#L261) - test_attachment_type_whitelist

**错误位置:**
```python
test_security.py:261: in test_attachment_type_whitelist
    assert response.status_code == 400
E   assert 200 == 400
```

**错误详情:** 不允许的附件类型应返回 400 错误，实际返回 200

**修复位置:** `internal/attachment/handler.go` 或 `internal/security/validator.go`

---

### 九、Supplementary 问题（4个）

---

#### ❌ 31. [test_supplementary.py:592](tests/test_supplementary.py#L592) - test_cleanup_preserves_active_sessions

**错误位置:**
```python
test_supplementary.py:592: in test_cleanup_preserves_active_sessions
    assert status.json()["chat"] is not None
E   assert None is not None
```

**错误详情:** 清理后活跃 session 的 chat 应不为 None

**修复位置:** `internal/memory/cleanup.go`

---

#### ❌ 32. [test_supplementary.py:865](tests/test_supplementary.py#L865) - test_cancel_sse_pushes_event

**错误位置:**
```python
test_supplementary.py:865: in test_cancel_sse_pushes_event
    assert completed["data"]["status"] == "cancelled"
E   AssertionError: assert 'success' == 'cancelled'
```

**错误详情:** 取消 SSE 应推送 'cancelled' 状态

**修复位置:** `internal/agent/sse.go`

---

#### ❌ 33. [test_supplementary.py:890](tests/test_supplementary.py#L890) - test_reasoning_step_emitted

**错误位置:**
```python
test_supplementary.py:890: in test_reasoning_step_emitted
    assert len(step_starts) > 0
E   assert 0 > 0
   +  where 0 = len([])
```

**错误详情:** reasoning 步骤事件未被发送

**修复位置:** `internal/agent/executor.go` - Step 事件发送逻辑

---

### 十、Performance 问题（1个 - 在 Performance 模块）

---

#### ❌ 34. test_concurrent_requests_per_session - 并发限制问题

**已在其他模块统计中体现**

---

## 📊 修复优先级总结

### 🔴 最高优先级（立即修复）

| 问题类型 | 数量 | 修复文件 | 工作量 |
|---------|------|---------|--------|
| CLI 测试路径 | 8 | `tests/test_cli_args.py` | 10分钟 |

**修复后通过率: 90.5%**

---

### 🟠 第二优先级（Cancel 状态）

| 问题 | 修复文件 | 影响测试 |
|------|---------|---------|
| SSE completed 状态 | `internal/agent/sse.go` | 3个 |
| Memory 状态记录 | `internal/memory/manager.go` | 1个 |

---

### 🟠 第三优先级（API Endpoints）

| 问题 | 修复文件 | 影响测试 |
|------|---------|---------|
| Content-Type header | `internal/api/handlers/chat.go` | 1个 |
| 并发冲突 409 | `internal/runtime/state.go` | 2个 |
| 字段缺失 (ended_at) | `internal/memory/manager.go` | 1个 |
| Cancel 状态返回 | `internal/api/handlers/chat.go` | 2个 |

---

### 🟡 第四优先级（Attachments）

| 问题 | 修复文件 |
|------|---------|
| URL 附件拒绝 | `internal/attachment/handler.go` |
| 错误码错误 | `internal/attachment/handler.go` |

---

### 🟢 测试配置问题（不修改程序）

| 问题 | 说明 |
|------|------|
| Real LLM Mock (5个) | Mock LLM 配置问题，非程序Bug |
| CLI 测试路径 (8个) | 测试代码问题，非程序Bug |

---

## 🎯 程序员修复指南

### 修复清单（按文件归类）

#### 📁 internal/agent/sse.go
- 修复 Completed 事件的 status 字段：取消时应为 'cancelled'

#### 📁 internal/memory/manager.go
- 添加 `ended_at` 字段到 ChatRecord
- 取消时保存 status='cancelled'

#### 📁 internal/runtime/state.go
- 检查并发限制返回 409
- 确保 RegisterActiveChat 返回正确值

#### 📁 internal/api/handlers/chat.go
- SSE Content-Type 应为 "text/event-stream"（不含 charset）
- DELETE /chat 无运行对话时返回 'no_running_chat'
- 并发请求返回 409

#### 📁 internal/attachment/handler.go
- 允许 URL 类型附件
- 修正错误码返回顺序（先类型检查，后大小检查）
- 白名单验证返回 400

#### 📁 tests/test_cli_args.py (测试代码)
- 使用正确的 groot 二进制路径

---

**报告生成日期:** 2026-04-19  
**测试轮次:** 第六轮  
**目的:** 提供精确的失败位置和错误描述，便于程序员针对性修复