# Groot 测试报告（第十一轮）

**测试日期:** 2026-04-19  
**测试时长:** 157秒  
**测试结果:** 239 通过, 23 失败, 8 跳过  
**通过率:** 88.5%

---

## 📊 失败测试分类

| 类型 | 数量 | 说明 |
|------|------|------|
| **程序Bug** | **17个** | 需要修改代码 |
| **Mock LLM配置** | **6个** | 非程序问题，测试配置使用Mock |

---

# 失败测试详情（17个程序Bug）

---

## 失败测试: test_new_session_with_attachment

**文件:** `tests/test_api_endpoints.py:96`

**期望:** 带附件的请求应包含 `file_read` 步骤事件
**实际:** `file_read_steps` 长度为 0，没有发送任何步骤事件

**HTTP 状态码:** 200（请求成功，但缺少步骤事件）

**SSE 事件顺序:**
```
["intent", "progress", "completed"]
```

**completed 事件内容:**
```json
{
  "status": "success",
  "duration": "0s",
  "result": "任务执行完成，但未获得明确结果",
  "round": 1
}
```

**问题:** 附件处理流程没有发送 `step_start` 和 `step_end` SSE事件

**一句话:** 测试名 + 期望有步骤事件 + 实际步骤列表为空 + 缺少附件处理的SSE事件发送逻辑

---

## 失败测试: test_multi_attachments

**文件:** `tests/test_api_endpoints.py:119`

**期望:** HTTP 状态码 200
**实际:** HTTP 状态码 400

**请求payload:**
```json
{
  "instruction": "对比分析这两个文件",
  "attachments": [
    {"type": "file", "name": "file1.csv", "content": "bmFtZSxhZ2UsY2l0eQo..."},
    {"type": "file", "name": "file2.pdf", "content": "JVBERi0xLjQK..."},
    {"type": "url", "name": "external.pdf", "url": "https://example.com/doc.pdf"}
  ]
}
```

**问题:** 多附件请求被拒绝，返回400错误。可能原因：
1. URL类型不在白名单
2. 附件数量限制问题

**一句话:** 测试名 + 期望200 + 实际400 + URL附件类型或多附件数量被拒绝

---

## 失败测试: test_concurrent_session_conflict

**文件:** `tests/test_api_endpoints.py:240`

**期望:** HTTP 状态码 409（Conflict）
**实际:** HTTP 状态码 200

**测试流程:**
1. 发送第一个请求（长任务）
2. 获取 session_id
3. 使用相同 session_id 发送第二个请求
4. **期望:** 第二个请求返回 409 Conflict
5. **实际:** 第二个请求返回 200 成功

**问题:** 并发检查逻辑缺失，同一session可以同时执行多个请求

**一句话:** 测试名 + 期望409 + 实际200 + 缺少并发冲突检查逻辑

---

## 失败测试: test_cancel_no_running_chat

**文件:** `tests/test_api_endpoints.py:314`

**期望:** `status == "no_running_chat"`
**实际:** `status == "success"`

**请求:** DELETE `/chat/{session_id}`（session_id不存在活跃对话）

**实际返回:**
```json
{
  "status": "success"
}
```

**期望返回:**
```json
{
  "status": "no_running_chat",
  "message": "No active chat to cancel"
}
```

**问题:** 无活跃对话时，取消接口应该提示用户，而不是返回success

**一句话:** 测试名 + 期望no_running_chat + 实际success + 无活跃对话时应返回特定状态

---

## 失败测试: test_get_running_status

**文件:** `tests/test_api_endpoints.py:342`

**期望:** `status == "success"`
**实际:** `status == "idle"`

**请求:** GET `/chat/status/{session_id}`（对话正在执行时查询）

**实际返回:**
```json
{
  "status": "idle",
  "chat": null
}
```

**问题:** 活跃对话状态查询返回 `idle` 和 `chat=null`，无法正确获取运行状态

**一句话:** 测试名 + 期望success + 实际idle + GetActiveChat返回null导致状态错误

---

## 失败测试: test_get_chat_detail

**文件:** `tests/test_api_endpoints.py:403`

**期望:** `chat` 字典包含 `"ended_at"` 字段
**实际:** `chat` 字典缺少 `"ended_at"` 字段

**实际返回的chat字段:**
```json
{
  "attachments": [],
  "caller": "",
  "chat_id": "chat_20260419154249162",
  "duration": 0,
  "instruction": "帮我写一个函数",
  "result": "...",
  "round": 1,
  "started_at": "2026-04-19T...",
  "status": "completed"
}
```

**缺少的字段:** `ended_at`

**问题:** ChatRecord 结构体缺少 ended_at 字段定义

**一句话:** 测试名 + 期望ended_at字段 + 实际缺失 + ChatRecord结构体缺少字段

---

## 失败测试: test_url_attachment

**文件:** `tests/test_attachments.py:60`

**期望:** HTTP 状态码 200
**实际:** HTTP 状态码 400

**请求payload:**
```json
{
  "instruction": "获取这个URL的内容",
  "attachments": [
    {"type": "url", "name": "external.pdf", "url": "https://example.com/doc.pdf"}
  ]
}
```

**问题:** URL类型附件不在白名单，被拒绝

**一句话:** 测试名 + 期望200 + 实际400 + URL类型不在allowed_types白名单

---

## 失败测试: test_409_error_format

**文件:** `tests/test_errors.py:70`

**期望:** HTTP 状态码 409
**实际:** HTTP 状态码 200

**问题:** 与 test_concurrent_session_conflict 相同，缺少并发检查

**一句话:** 测试名 + 期望409 + 实际200 + 缺少并发冲突检查逻辑

---

## 失败测试: test_error_contains_session_id_when_relevant

**文件:** `tests/test_errors.py:311`

**期望:** 返回有效的JSON响应
**实际:** 返回SSE流内容，无法解析为JSON

**实际响应内容（SSE格式）:**
```
event: intent
data: {"timestamp":"2026-04-19T07:43:25Z"}

event: progress
data: {"message":"[NodeRunError] Post ..."}

event: completed
data: {"duration":"0s","result":"任务执行完成，但未获得明确结果","round":2,"status":"success"}
```

**问题:** 并发请求时应该返回JSON错误响应，而不是SSE流

**一句话:** 测试名 + 期望JSON + 实际SSE流 + 并发错误应返回JSON而非SSE

---

## 失败测试: test_status_cancelled

**文件:** `tests/test_memory.py:435`

**期望:** `messages[0]["status"] == "cancelled"`
**实际:** `messages[0]["status"] == "completed"`

**测试流程:**
1. 发送长任务请求
2. 发送取消请求
3. 查询历史记录
4. **期望:** status 为 "cancelled"
5. **实际:** status 为 "completed"

**Memory中保存的状态:**
```json
{
  "status": "completed"  // ← 错误，应该是 "cancelled"
}
```

**问题:** 取消后Memory保存的状态错误，没有设置为 "cancelled"

**一句话:** 测试名 + 期望cancelled + 实际completed + 取消时状态保存错误

---

## 失败测试: test_cancelled_completed_event

**文件:** `tests/test_sse_events.py:338`

**期望:** completed事件 `status == "cancelled"`
**实际:** completed事件 `status == "success"`

**completed事件内容:**
```json
{
  "status": "success",  // ← 错误，应该是 "cancelled"
  "duration": "0s",
  "round": 1
}
```

**测试流程:**
1. 启动长任务
2. 发送取消请求
3. 等待SSE完成
4. 检查completed事件

**问题:** SSE WriteCompleted 函数没有传递正确的取消状态

**一句话:** 测试名 + 期望cancelled + 实际success + SSE completed事件状态错误

---

## 失败测试: test_register_active_chat

**文件:** `tests/test_runtime_state.py:39`

**期望:** `data["chat"] is not None`
**实际:** `data["chat"] == None`

**请求:** GET `/chat/status/{session_id}`

**实际返回:**
```json
{
  "status": "idle",
  "chat": null
}
```

**问题:** GetActiveChat 返回 null，活跃对话状态未正确记录

**一句话:** 测试名 + 期望chat不为null + 实际null + GetActiveChat返回null

---

## 失败测试: test_is_running_check

**文件:** `tests/test_runtime_state.py:66`

**期望:** `status1.json()["chat"]["status"] == "running"`
**实际:** TypeError: 'NoneType' object is not subscriptable

**问题:** 与 test_register_active_chat 相同，chat为null导致无法访问status

**一句话:** 测试名 + 期望running状态 + 实际TypeError(chat为null) + GetActiveChat返回null

---

## 失败测试: test_cleanup_preserves_active_sessions

**文件:** `tests/test_supplementary.py:592`

**期望:** `status.json()["chat"] is not None`
**实际:** `status.json()["chat"] == None`

**问题:** 与 test_register_active_chat 相同

**一句话:** 测试名 + 期望chat不为null + 实际null + GetActiveChat返回null

---

## 失败测试: test_cancel_interrupts_llm_call

**文件:** `tests/test_supplementary.py:815`

**期望:** completed事件 `status == "cancelled"`
**实际:** completed事件 `status == "success"`

**completed事件内容:**
```json
{
  "status": "success",
  "duration": "0s",
  "round": 1
}
```

**问题:** 与 test_cancelled_completed_event 相同

**一句话:** 测试名 + 期望cancelled + 实际success + SSE completed事件状态错误

---

## 失败测试: test_cancel_sse_pushes_event

**文件:** `tests/test_supplementary.py:865`

**期望:** completed事件 `status == "cancelled"`
**实际:** completed事件 `status == "success"`

**问题:** 与 test_cancelled_completed_event 相同

**一句话:** 测试名 + 期望cancelled + 实际success + SSE completed事件状态错误

---

## 失败测试: test_reasoning_step_emitted

**文件:** `tests/test_supplementary.py:890`

**期望:** `len(step_starts) > 0`
**实际:** `len(step_starts) == 0`

**SSE事件顺序:**
```
["intent", "progress", "completed"]  // ← 缺少 step_start
```

**问题:** ReAct执行流程没有发送 reasoning 步骤的 step_start/step_end 事件

**一句话:** 测试名 + 期望有step_start事件 + 实际事件列表为空 + 缺少reasoning步骤SSE事件

---

# Mock LLM 配置问题（6个 - 非程序Bug）

---

## 失败测试: test_real_llm_code_generation

**文件:** `tests/test_real_llm.py:66`

**期望:** result 包含 `"def"` 或 `"function"` 或 `"fibonacci"`
**实际:** `result == "任务执行完成，但未获得明确结果"`

**completed事件内容:**
```json
{
  "status": "success",
  "result": "任务执行完成，但未获得明确结果",
  "round": 1
}
```

**原因:** 测试配置使用Mock LLM，返回固定消息，非真实LLM调用

**一句话:** Mock LLM配置问题 + 需要真实API Key才能通过

---

以下5个测试相同原因（Mock LLM配置）：

| 测试名 | 文件 | 期望内容 | 实际返回 |
|-------|------|---------|---------|
| test_real_llm_json_output | test_real_llm.py:93 | JSON关键词 | "任务执行完成，但未获得明确结果" |
| test_real_llm_two_round_conversation | test_real_llm.py:142 | "42" | "任务执行完成，但未获得明确结果" |
| test_real_llm_analysis_task | test_real_llm.py:302 | "人工智能" | "任务执行完成，但未获得明确结果" |
| test_real_llm_translation_task | test_real_llm.py:326 | "Machine learning" | "任务执行完成，但未获得明确结果" |
| test_real_llm_math_problem | test_real_llm.py:347 | "957" | "任务执行完成，但未获得明确结果" |

**一句话:** 这6个测试使用Mock LLM配置，非程序Bug，需要真实API Key才能通过

---

# Bug修复清单（按优先级排序）

---

## 🔴 优先级1: Cancel状态处理（5个Bug）

| Bug | 测试名 | 修复位置 |
|-----|-------|---------|
| 取消状态保存错误 | test_status_cancelled | executor.go + memory.go |
| SSE completed状态 | test_cancelled_completed_event | sse.go |
| 取消中断LLM | test_cancel_interrupts_llm_call | executor.go |
| 取消SSE推送 | test_cancel_sse_pushes_event | sse.go |
| 无对话取消提示 | test_cancel_no_running_chat | handlers/chat.go |

**核心问题:** 取消时 `status` 应设置为 `"cancelled"`，不是 `"success"` 或 `"completed"`

---

## 🟠 优先级2: 并发冲突检查（3个Bug）

| Bug | 测试名 | 修复位置 |
|-----|-------|---------|
| 并发返回409 | test_concurrent_session_conflict | handlers/chat.go |
| 409错误格式 | test_409_error_format | handlers/chat.go |
| 409返回JSON | test_error_contains_session_id_when_relevant | handlers/chat.go |

**核心问题:** 同一session并发请求应返回409，不是200或SSE流

---

## 🟠 优先级3: Runtime State返回（3个Bug）

| Bug | 测试名 | 修复位置 |
|-----|-------|---------|
| chat返回null | test_register_active_chat | runtime/state.go |
| running检查失败 | test_is_running_check | runtime/state.go |
| 清理后状态丢失 | test_cleanup_preserves_active_sessions | runtime/state.go |

**核心问题:** GetActiveChat 应返回活跃对话信息，不是null

---

## 🟡 优先级4: SSE步骤事件（2个Bug）

| Bug | 测试名 | 修复位置 |
|-----|-------|---------|
| 附件步骤缺失 | test_new_session_with_attachment | executor.go |
| reasoning步骤缺失 | test_reasoning_step_emitted | executor.go |

**核心问题:** 附件处理和ReAct流程应发送 step_start/step_end 事件

---

## 🟡 优先级5: 其他功能（2个Bug）

| Bug | 测试名 | 修复位置 |
|-----|-------|---------|
| ended_at字段缺失 | test_get_chat_detail | memory/types.go |
| URL类型附件拒绝 | test_url_attachment + test_multi_attachments | attachment/validator.go |

---

# 总结

| 类型 | 数量 | 说明 |
|------|------|------|
| **程序Bug** | 17 | 需要修改代码 |
| **Mock LLM** | 6 | 测试配置问题 |

**修复17个Bug后，预期通过率: 94.8%（256/270）**

---

**一句话定位所有Bug:**

1. **Cancel:** 取消时status应为cancelled
2. **并发:** 同session并发应返回409
3. **Runtime:** GetActiveChat不应返回null
4. **SSE:** 附件和ReAct应发送步骤事件
5. **字段:** ChatRecord缺少ended_at
6. **附件:** URL类型应加入白名单