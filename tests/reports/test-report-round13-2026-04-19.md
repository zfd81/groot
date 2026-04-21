# Groot 测试报告（第十三轮）

**测试日期:** 2026-04-19
**测试时长:** 156.82秒
**测试结果:** 239 通过, 23 失败, 8 跳过
**通过率:** 88.5%

---

# 失败测试详情

---

## 测试名称: test_new_session_with_attachment

**文件:** `tests/test_api_endpoints.py:96`

**期望值:**
- `len(file_read_steps) > 0`

**实际值:**
- `len(file_read_steps) == 0`

**完整响应JSON:** (测试未直接获取JSON，SSE事件列表为空)

---

## 测试名称: test_multi_attachments

**文件:** `tests/test_api_endpoints.py:119`

**期望值:**
- HTTP 状态码: `200`

**实际值:**
- HTTP 状态码: `400`

**完整响应JSON:** (400错误响应，具体内容需查看API返回)

---

## 测试名称: test_concurrent_session_conflict

**文件:** `tests/test_api_endpoints.py:240`

**期望值:**
- HTTP 状态码: `409`

**实际值:**
- HTTP 状态码: `200`

**完整响应JSON:** (第二个请求返回200成功接受，而非409冲突)

---

## 测试名称: test_cancel_no_running_chat

**文件:** `tests/test_api_endpoints.py:314`

**期望值:**
```json
{
  "status": "no_running_chat"
}
```

**实际值:**
```json
{
  "status": "success"
}
```

---

## 测试名称: test_get_running_status

**文件:** `tests/test_api_endpoints.py:342`

**期望值:**
```json
{
  "status": "success",
  "chat": {
    "chat_id": "...",
    "status": "running",
    ...
  }
}
```

**实际值:**
```json
{
  "status": "idle",
  "chat": null
}
```

---

## 测试名称: test_get_chat_detail

**文件:** `tests/test_api_endpoints.py:403`

**期望值:**
- chat字段包含 `"ended_at"` 字段

**实际值:**
```json
{
  "attachments": [],
  "caller": "",
  "chat_id": "chat_20260419162111499",
  "duration": 0,
  "instruction": "帮我写一个函数",
  "result": "...",
  "round": 1,
  "started_at": "2026-04-19T...",
  "status": "completed"
}
```

**缺少字段:** `ended_at`

---

## 测试名称: test_url_attachment

**文件:** `tests/test_attachments.py:60`

**期望值:**
- HTTP 状态码: `200`

**实际值:**
- HTTP 状态码: `400`

---

## 测试名称: test_409_error_format

**文件:** `tests/test_errors.py:70`

**期望值:**
- HTTP 状态码: `409`

**实际值:**
- HTTP 状态码: `200`

---

## 测试名称: test_error_contains_session_id_when_relevant

**文件:** `tests/test_errors.py:311`

**期望值:**
- 返回有效JSON响应（包含session_id）

**实际值:**
- 返回SSE流内容，无法解析为JSON
- `JSONDecodeError: Expecting value: line 1 column 1`

---

## 测试名称: test_status_cancelled

**文件:** `tests/test_memory.py:435`

**期望值:**
```json
{
  "status": "cancelled"
}
```

**实际值:**
```json
{
  "status": "completed"
}
```

---

## 测试名称: test_register_active_chat

**文件:** `tests/test_runtime_state.py:39`

**期望值:**
```json
{
  "chat": {
    "chat_id": "...",
    "status": "running",
    ...
  }
}
```

**实际值:**
```json
{
  "chat": null
}
```

---

## 测试名称: test_is_running_check

**文件:** `tests/test_runtime_state.py:66`

**期望值:**
```json
{
  "chat": {
    "status": "running"
  }
}
```

**实际值:**
- `TypeError: 'NoneType' object is not subscriptable`
- (chat为null，无法访问status)

---

## 测试名称: test_cancelled_completed_event

**文件:** `tests/test_sse_events.py:338`

**期望值:**
```json
{
  "status": "cancelled",
  "duration": "0s",
  "round": 1
}
```

**实际值:**
```json
{
  "status": "success",
  "duration": "0s",
  "round": 1
}
```

---

## 测试名称: test_cleanup_preserves_active_sessions

**文件:** `tests/test_supplementary.py:592`

**期望值:**
```json
{
  "chat": { ... }
}
```

**实际值:**
```json
{
  "chat": null
}
```

---

## 测试名称: test_cancel_interrupts_llm_call

**文件:** `tests/test_supplementary.py:815`

**期望值:**
```json
{
  "status": "cancelled"
}
```

**实际值:**
```json
{
  "status": "success"
}
```

---

## 测试名称: test_cancel_sse_pushes_event

**文件:** `tests/test_supplementary.py:865`

**期望值:**
```json
{
  "status": "cancelled"
}
```

**实际值:**
```json
{
  "status": "success"
}
```

---

## 测试名称: test_reasoning_step_emitted

**文件:** `tests/test_supplementary.py:890`

**期望值:**
- `len(step_starts) > 0`

**实际值:**
- `len(step_starts) == 0`

---

# Mock LLM 配置问题（6个）

以下测试失败是Mock配置问题，非程序Bug：

| 测试名称 | 文件 | 说明 |
|---------|------|------|
| test_real_llm_code_generation | test_real_llm.py:66 | Mock返回固定消息 |
| test_real_llm_json_output | test_real_llm.py:93 | Mock返回固定消息 |
| test_real_llm_two_round_conversation | test_real_llm.py:142 | Mock返回固定消息 |
| test_real_llm_analysis_task | test_real_llm.py:302 | Mock返回固定消息 |
| test_real_llm_translation_task | test_real_llm.py:326 | Mock返回固定消息 |
| test_real_llm_math_problem | test_real_llm.py:347 | Mock返回固定消息 |

---

# 总结

**通过率:** 88.5% (239/270)
**程序Bug:** 17个（与之前相同，未修复）
**Mock配置问题:** 6个

---

**报告生成:** 2026-04-19