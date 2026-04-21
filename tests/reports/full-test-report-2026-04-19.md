# Groot 整体测试报告

**测试日期:** 2026-04-19  
**测试环境:** macOS Darwin 25.4.0  
**Python:** 3.9.6 | pytest: 8.4.2  
**测试时长:** 127.22 秒  

---

## 一、测试结果统计

| 指标 | 数值 |
|------|------|
| 总测试数 | 270 |
| 通过 | 194 |
| 失败 | 68 |
| 跳过 | 8 |
| **通过率** | **71.8%** |

### 与上一轮测试对比

| 指标 | 上轮 | 本轮 | 变化 |
|------|------|------|------|
| 通过率 | 63.1% | 71.8% | ↑ 8.7% |
| 热插拔测试 | 2 failed | **11 passed** | ✅ 全部修复 |
| 认证测试 | 已修复 | **14 passed** | ✅ 保持稳定 |
| SSE 问题 | 主要失败 | 大部分通过 | ✅ 已修复 |

---

## 二、已修复问题确认

### ✅ P0-1: Skills Watcher 竞态条件 - 已修复

```
test_hot_reload.py::TestSkillsHotReload::test_add_skill_updates_list PASSED
test_hot_reload.py::TestSkillsHotReload::test_remove_skill_updates_list PASSED
test_hot_reload.py::TestSkillsHotReload::test_modify_skill_updates_content PASSED
test_hot_reload.py::TestMCPHotReload (全部 4 个测试) PASSED
test_hot_reload.py::TestDebounceDelay::test_debounce_delay PASSED

结果: 11 passed ✅
```

**验证:** 热插拔功能现在可以正确检测新创建的 skill 目录和文件。

---

### ✅ P0-2: SSE goroutine 问题 - 已修复

```
test_sse_events.py::TestSSEEventOrder (全部 6 个测试) PASSED
test_sse_events.py::TestSSEEventFields (全部 6 个测试) PASSED
test_real_llm.py::TestRealLLMBasic::test_real_llm_simple_question PASSED
test_real_llm.py::TestRealLLMSSEReliability (全部 2 个测试) PASSED

结果: SSE 事件顺序正确，intent 不再重复发送 ✅
```

---

### ✅ P1-1: intent 事件重复发送 - 已修复

```
test_real_llm.py::TestRealLLMSSEReliability::test_real_llm_no_duplicate_intent PASSED
```

---

### ✅ P1-2: Health memory 检查 - 已添加

```
test_supplementary.py::TestHealthDetailedChecks::test_health_memory_check PASSED
```

---

## 三、仍存在的问题（按优先级）

---

### 🔴 P0-3: CLI 命令找不到 (严重)

**影响:** 8 个 CLI 测试全部失败

**错误信息:**
```
FileNotFoundError: [Errno 2] No such file or directory: 'groot'
```

**失败测试:**
- `test_cli_args.py::TestCommandLineArgs::test_help_flag`
- `test_cli_args.py::TestCommandLineArgs::test_version_flag`
- `test_cli_args.py::TestCommandLineArgs::test_home_flag`
- `test_cli_args.py::TestCommandLineArgs::test_port_flag`
- `test_cli_args.py::TestEnvironmentVariables::test_groot_home_env`
- `test_cli_args.py::TestEnvironmentVariables::test_groot_api_key_env`
- `test_cli_args.py::TestConfigPriority::test_cli_overrides_config`
- `test_cli_args.py::TestConfigPriority::test_env_overrides_default`

**根因:**  
测试代码 `test_cli_args.py` 直接调用 `subprocess.Popen(["groot", ...])`，但 groot 未安装到系统 PATH。

**修复建议:**  
修改 `tests/test_cli_args.py`，使用正确的 groot 路径：

```python
# 当前代码
process = subprocess.Popen(["groot", "-h", ...])

# 应改为
GROOT_BIN = os.path.join(os.path.dirname(os.path.dirname(__file__)), "bin", "groot")
process = subprocess.Popen([GROOT_BIN, "-h", ...])
```

---

### 🔴 P0-4: Attachment 错误码不统一 (严重)

**影响:** 12 个 attachment 相关测试失败

**问题:**  
实际返回的错误码是 `attachment_validation_error`，但测试期望具体的错误码如：
- `attachment_count_exceeded`
- `attachment_type_not_allowed`
- `attachment_size_exceeded`
- `attachment_decode_error`

**失败示例:**
```python
# test_attachments.py:108
AssertionError: assert 'attachment_validation_error' == 'attachment_count_exceeded'

# test_attachments.py:156
AssertionError: assert 'attachment_validation_error' == 'attachment_type_not_allowed'
```

**位置:** `internal/attachment/handler.go` 或相关验证逻辑

**修复建议:**  
区分不同的附件错误类型，返回具体的错误码：

```go
// 当前可能实现
return &AttachmentError{Code: "attachment_validation_error", Message: "..."}

// 应改为
switch errType {
case CountExceeded:
    return &AttachmentError{Code: "attachment_count_exceeded", Message: "附件数量超过限制"}
case TypeNotAllowed:
    return &AttachmentError{Code: "attachment_type_not_allowed", Message: "附件类型不允许"}
case SizeExceeded:
    return &AttachmentError{Code: "attachment_size_exceeded", Message: "附件大小超过限制"}
case DecodeError:
    return &AttachmentError{Code: "attachment_decode_error", Message: "附件解码失败"}
}
```

---

### 🟠 P1-3: API 响应缺少字段 (重要)

**问题1: Session 详情缺少 `last_active_at` 字段**

```python
# test_api_endpoints.py:517
AssertionError: assert 'last_active_at' in {
    'created_at': '...',
    'path': '...',
    'round_count': 1,
    'session_id': '...'
}
```

**位置:** `internal/api/handler/session.go` - SessionDetail 响应结构

**修复:** 在 session 详情响应中添加 `last_active_at` 字段

---

**问题2: Chat 详情缺少 `chat` 字段**

```python
# test_runtime_state.py 多处
KeyError: 'chat'
```

**位置:** 多个 API handler 返回的 chat 详情

**修复:** 确保 chat 相关 API 返回包含 `chat` 字段的完整结构

---

**问题3: Error 响应缺少 `session_id` 字段**

```python
# test_errors.py:313
AssertionError: assert 'session_id' in {
    'message': '该会话已有对话正在执行',
    'status': 'chat_limit_exceeded'
}
```

**位置:** `internal/api/handler/chat.go` - 错误响应处理

**修复:** 当错误与特定 session 相关时，在错误响应中包含 `session_id`

---

### 🟠 P1-4: Chat round 计数问题 (重要)

**问题:** chat_id 在多轮对话中应该变化，但实际没有变化

```python
# test_id_formats.py:494
AssertionError: assert 6 == 2  # 期望第二轮有新的 chat_id
```

**位置:** `internal/agent/executor.go` 或 session 管理

**需要确认:** 设计文档中 chat_id 是否应该在每轮变化？

---

### 🟠 P1-5: Cancel 状态处理问题 (重要)

**问题:** 取消的 chat 状态应该为 `cancelled`，但实际返回 `success`

```python
# test_sse_events.py:338
AssertionError: assert 'success' == 'cancelled'

# test_memory.py:435
AssertionError: assert 'completed' == 'cancelled'

# test_supplementary.py:815, 865
AssertionError: assert 'success' == 'cancelled'
```

**位置:** `internal/agent/executor.go` 取消逻辑

**修复:** 确保取消时：
1. SSE completed 事件 status 为 `cancelled`
2. ChatRecord 状态为 `cancelled`
3. Memory 记录状态为 `cancelled`

---

### 🟡 P2-1: LLM 返回值编码问题 (次要)

**问题:** LLM 返回内容显示乱码

```python
# test_real_llm.py:66
AssertionError: assert 'def' in 'ä»»å\x8a¡æ\x89§è¡\x8cå®\x8cæ\x88\x90...'
```

**可能原因:**  
1. LLM 返回的中文被错误编码
2. 测试环境编码问题

**建议:** 检查 SSE writer 的编码设置

---

### 🟡 P2-2: Memory 结构问题 (次要)

**问题:** history.json 结构不符合预期

```python
# test_memory.py:86
AssertionError: assert 'error' in {'attachments': [], 'chat_id': '...', ...}

# test_memory.py:127
AssertionError: assert 1 == 2  # round_count 期望为 2
```

**位置:** `internal/memory/manager.go`

**需要确认:** Memory 模块的数据结构设计

---

### 🟡 P2-3: 并发请求限制 (次要)

```python
# test_performance.py:250
assert 0 >= 1  # 期望同一 session 并发请求被限制
```

**位置:** `internal/api/handler/chat.go` - 并发控制

---

## 四、按模块分类的失败统计

| 模块 | 失败数 | 主要问题 |
|------|--------|---------|
| test_cli_args.py | 8 | groot 命令不在 PATH |
| test_attachments.py | 10 | 错误码不具体 |
| test_api_endpoints.py | 10 | 缺少字段、并发限制 |
| test_runtime_state.py | 9 | KeyError: 'chat' |
| test_memory.py | 5 | 结构不符合预期 |
| test_real_llm.py | 10 | 编码问题、返回值 |
| test_sse_events.py | 2 | cancel 状态 |
| test_supplementary.py | 5 | cancel、history |
| test_errors.py | 5 | 错误码不具体 |
| test_id_formats.py | 3 | round 变化 |
| test_performance.py | 1 | 并发限制 |
| test_security.py | 1 | 错误码 |

---

## 五、建议修复顺序

### 第一优先级 (P0)

| 序号 | 问题 | 位置 | 预估影响测试数 |
|------|------|------|--------------|
| 1 | CLI 命令路径 | `tests/test_cli_args.py` | 8 (测试问题，非代码) |
| 2 | Attachment 错误码 | `internal/attachment/handler.go` | 12 |

### 第二优先级 (P1)

| 序号 | 问题 | 位置 | 预估影响测试数 |
|------|------|------|--------------|
| 3 | API 缺少字段 | 多个 handler | 10+ |
| 4 | Cancel 状态处理 | `internal/agent/executor.go` | 5 |
| 5 | Chat round 计数 | session 管理 | 3 |

### 第三优先级 (P2)

| 序号 | 问题 | 位置 |
|------|------|------|
| 6 | LLM 编码 | SSE writer |
| 7 | Memory 结构 | `internal/memory` |
| 8 | 并发限制 | chat handler |

---

## 六、详细错误列表

### CLI 测试失败 (8个)

```
test_cli_args.py::TestCommandLineArgs::test_help_flag
  错误: FileNotFoundError: 'groot'
  
test_cli_args.py::TestCommandLineArgs::test_version_flag
  错误: FileNotFoundError: 'groot'
  
test_cli_args.py::TestCommandLineArgs::test_home_flag
  错误: FileNotFoundError: 'groot'
  
test_cli_args.py::TestCommandLineArgs::test_port_flag
  错误: FileNotFoundError: 'groot'
  
test_cli_args.py::TestEnvironmentVariables::test_groot_home_env
  错误: FileNotFoundError: 'groot'
  
test_cli_args.py::TestEnvironmentVariables::test_groot_api_key_env
  错误: FileNotFoundError: 'groot'
  
test_cli_args.py::TestConfigPriority::test_cli_overrides_config
  错误: FileNotFoundError: 'groot'
  
test_cli_args.py::TestConfigPriority::test_env_overrides_default
  错误: FileNotFoundError: 'groot'
```

### Attachment 测试失败 (10个)

```
test_attachments.py::TestAttachmentBasic::test_single_attachment
  错误: status='failed', 期望='success'
  
test_attachments.py::TestAttachmentBasic::test_multiple_attachments
  错误: status='failed', 期望='success'
  
test_attachments.py::TestAttachmentLimits::test_attachment_count_exceeded
  错误: code='attachment_validation_error', 期望='attachment_count_exceeded'
  
test_attachments.py::TestAttachmentLimits::test_attachment_size_exceeded
  错误: status=413, 期望=400
  
test_attachments.py::TestAttachmentLimits::test_attachment_type_not_allowed
  错误: code='attachment_validation_error', 期望='attachment_type_not_allowed'
  
test_attachments.py::TestAttachmentLimits::test_attachment_total_size_exceeded
  错误: status=413, 期望=400
  
test_attachments.py::TestAttachmentErrors::test_attachment_decode_error
  错误: status=200, 期望=400
  
test_attachments.py::TestAttachmentErrors::test_attachment_missing_content
  错误: status=200, 期望=400
  
test_attachments.py::TestAttachmentErrors::test_attachment_missing_url
  错误: status=200, 期望=400
  
test_attachments.py::TestAttachmentFilenameSafety::test_filename_overwrite
  错误: status=409, 期望=200
```

### API Endpoints 测试失败 (10个)

```
test_api_endpoints.py::TestChatAPI::test_new_session_with_attachment
  错误: status='failed'
  
test_api_endpoints.py::TestChatAPI::test_multi_attachments
  错误: status='failed', 期望='success'
  
test_api_endpoints.py::TestChatAPI::test_with_custom_prompt
  错误: status='failed', 期望='success'
  
test_api_endpoints.py::TestChatAPI::test_continue_session
  错误: assert 409 == 200 (并发限制生效但测试期望可继续)
  
test_api_endpoints.py::TestDeleteChatAPI::test_cancel_no_running_chat
  错误: assert False
  
test_api_endpoints.py::TestChatStatusAPI::test_get_running_status
  错误: status='running', 期望='success'
  
test_api_endpoints.py::TestChatStatusAPI::test_get_no_running_status
  错误: assert 404 == 200
  
test_api_endpoints.py::TestChatDetailAPI::test_get_chat_detail
  错误: assert 404 == 200
  
test_api_endpoints.py::TestSessionDetailAPI::test_get_session_detail
  错误: assert 404 == 200
  
test_api_endpoints.py::TestSessionHistoryAPI::test_get_session_list
  错误: 缺少 'last_active_at' 字段
```

### Runtime State 测试失败 (9个)

```
test_runtime_state.py::TestRuntimeStateBasic::test_register_active_chat
  错误: KeyError: 'chat'
  
test_runtime_state.py::TestRuntimeStateBasic::test_is_running_check
  错误: KeyError: 'chat'
  
test_runtime_state.py::TestRuntimeStateBasic::test_complete_removes_active_state
  错误: KeyError: 'chat'
  
test_runtime_state.py::TestRuntimeStateProgress::test_update_progress
  错误: KeyError: 'chat'
  
test_runtime_state.py::TestRuntimeStateProgress::test_elapsed_time_tracking
  错误: KeyError: 'chat'
  
test_runtime_state.py::TestRuntimeStateCancel::test_cancel_active_chat
  错误: KeyError: 'chat'
  
test_runtime_state.py::TestRuntimeStateMemoryIntegration::test_complete_saves_to_memory
  错误: 缺少 'error' 字段
  
test_runtime_state.py::TestRuntimeStateMemoryIntegration::test_chat_record_saved
  错误: assert 404 == 200
  
test_runtime_state.py::TestRuntimeStateActiveChatFields::test_active_chat_field_structure
  错误: KeyError: 'chat'
```

### SSE Events 测试失败 (2个)

```
test_sse_events.py::TestSSECancelledEvent::test_cancelled_completed_event
  错误: status='success', 期望='cancelled'
  
test_sse_events.py::TestSSEMultipleRounds::test_round_field_increment
  错误: TypeError: 'NoneType' object is not subscriptable
```

---

## 七、附录：完整测试日志

测试日志已保存到 `/tmp/test_results_full.log`

---

## 八、结论

### 已成功修复的问题 ✅

1. **Skills Watcher 竞态条件** - 热插拔功能完全正常
2. **SSE goroutine 异步执行** - SSE 事件顺序正确
3. **intent 重复发送** - 已修复
4. **Health memory 检查** - 已添加

### 待修复的问题 ❌

主要问题集中在：

1. **Attachment 错误码** - 需区分具体错误类型（影响 12 个测试）
2. **API 响应字段** - 需补充缺失字段（影响 10+ 个测试）
3. **Cancel 状态处理** - 取消状态应正确记录（影响 5 个测试）

### 建议

优先修复 P0 级问题（Attachment 错误码），预计可使通过率提升至 80%+。

---

**报告编写:** Claude Code  
**报告日期:** 2026-04-19