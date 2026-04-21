# Groot 测试报告（第十四轮）

**测试日期:** 2026-04-19
**测试时长:** 156.94秒
**测试结果:** 240 通过, 22 失败, 8 跳过
**通过率:** 88.9% (比上一轮提升 0.4%)

---

## ✅ 已修复的Bug

| Bug | 测试 | 修复确认 |
|-----|------|---------|
| Bug #12 | test_get_chat_detail | ended_at字段存在 |
| Bug #13-14 | SSE步骤事件 | step_start/step_end正常发送 |
| Bug #15 | test_url_attachment | HTTP 200 (不再是400) |

---

## ❌ 仍失败的测试（16个程序Bug + 6个Mock配置）

---

### 一、Cancel状态处理（6个Bug）

#### 测试名称: test_cancel_running_chat

**文件:** `test_api_endpoints.py`

**期望值:**
```json
{
  "status": "cancelled"
}
```

**实际值:** SSE completed事件返回 `success`

---

#### 测试名称: test_status_cancelled

**文件:** `test_memory.py:435`

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

#### 测试名称: test_cancelled_completed_event

**文件:** `test_sse_events.py:338`

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

#### 测试名称: test_cancel_interrupts_llm_call

**文件:** `test_supplementary.py:815`

**期望值:** `cancelled`
**实际值:** `success`

---

#### 测试名称: test_cancel_sse_pushes_event

**文件:** `test_supplementary.py:865`

**期望值:** `cancelled`
**实际值:** `success`

---

#### 测试名称: test_cancel_active_chat

**文件:** `test_runtime_state.py`

**期望值:** 取消成功
**实际值:** 失败

---

### 二、并发冲突检查（3个Bug）

#### 测试名称: test_concurrent_session_conflict

**文件:** `test_api_endpoints.py:240`

**期望值:**
- HTTP 状态码: `409`

**实际值:**
- HTTP 状态码: `200`

---

#### 测试名称: test_409_error_format

**文件:** `test_errors.py:70`

**期望值:**
- HTTP 状态码: `409`

**实际值:**
- HTTP 状态码: `200`

---

#### 测试名称: test_error_contains_session_id_when_relevant

**文件:** `test_errors.py:311`

**期望值:**
- 返回JSON响应（包含session_id）

**实际值:**
- 返回SSE流，无法解析为JSON

---

### 三、Runtime State返回（3个Bug）

#### 测试名称: test_get_running_status

**文件:** `test_api_endpoints.py:342`

**期望值:**
```json
{
  "status": "success",
  "chat": {...}
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

#### 测试名称: test_register_active_chat

**文件:** `test_runtime_state.py:39`

**期望值:** chat不为null
**实际值:** `chat == null`

---

#### 测试名称: test_is_running_check

**文件:** `test_runtime_state.py:66`

**期望值:** chat.status == "running"
**实际值:** TypeError (chat为null)

---

#### 测试名称: test_cleanup_preserves_active_sessions

**文件:** `test_supplementary.py:592`

**期望值:** chat不为null
**实际值:** `chat == null`

---

### 四、SSE事件问题（2个新Bug）

#### 测试名称: test_progress_between_steps

**文件:** `test_sse_events.py`

**期望值:** 事件顺序包含 `step_end`
**实际值:** `['intent', 'step_start', 'progress']` (缺少step_end)

---

#### 测试名称: test_step_start_event_fields

**文件:** `test_sse_events.py:195`

**期望值:** `step["type"] in ["skill", "tool", "llm"]`
**实际值:** `step["type"] == "reasoning"`

**问题:** type字段新增了 "reasoning" 类型，测试未更新

---

### 五、ID格式问题（1个新Bug）

#### 测试名称: test_step_id_format

**文件:** `test_id_formats.py`

**期望值:** step_id格式符合规范
**实际值:** 格式不匹配

---

### 六、Mock LLM配置问题（6个 - 非程序Bug）

| 测试名 | 文件 | 说明 |
|-------|------|------|
| test_real_llm_code_generation | test_real_llm.py | Mock返回固定消息 |
| test_real_llm_json_output | test_real_llm.py | Mock返回固定消息 |
| test_real_llm_two_round_conversation | test_real_llm.py | Mock返回固定消息 |
| test_real_llm_analysis_task | test_real_llm.py | Mock返回固定消息 |
| test_real_llm_translation_task | test_real_llm.py | Mock返回固定消息 |
| test_real_llm_math_problem | test_real_llm.py | Mock返回固定消息 |

---

## 程序员分析验证

### 程序员认为的"测试时机问题"

调试脚本显示：
- URL附件返回HTTP 200（不是400）
- SSE步骤事件正常发送
- ended_at字段存在

**结论:** 程序员的分析可能基于旧版本。最新测试显示：
1. Execute确实同步执行很快
2. 但部分测试确实可以通过（如URL附件）

### 建议

**高优先级修复：**
1. Cancel状态处理（6个Bug） - 取消时status必须为 "cancelled"
2. 并发冲突检查（3个Bug） - 同session并发返回409
3. Runtime State（3个Bug） - GetActiveChat返回正确数据

**测试需要更新：**
1. `test_step_start_event_fields` - type字段应包含 "reasoning"

---

**报告生成:** 2026-04-19