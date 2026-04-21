# Groot 测试报告（第十二轮）

**测试日期:** 2026-04-19
**测试时长:** 157秒
**测试结果:** 239 通过, 23 失败, 8 跳过
**通过率:** 88.5%

---

## 📊 失败测试分类

| 类型 | 数量 | 说明 |
|------|------|------|
| **程序Bug** | **17个** | Bug未修复，与上一轮相同 |
| **Mock LLM配置** | **6个** | 非程序问题 |

---

## ⚠️ 重要发现

**本轮测试结果与上一轮（第十一轮）完全相同！**

这说明程序员可能：
1. 没有实际修改代码
2. 修改了但未部署/重启服务
3. 修改位置错误

---

# 失败测试详情（按用户要求格式）

---

## 失败测试 #1: test_new_session_with_attachment

**测试名称:** `test_new_session_with_attachment`
**文件位置:** `tests/test_api_endpoints.py:96`

**期望值:**
- `len(file_read_steps) > 0` (应包含文件读取步骤事件)

**实际值:**
- `len(file_read_steps) == 0` (没有任何步骤事件)

**一句话:** 带附件的请求应发送 `step_start/step_end` 事件，实际没有发送任何步骤事件

---

## 失败测试 #2: test_multi_attachments

**测试名称:** `test_multi_attachments`
**文件位置:** `tests/test_api_endpoints.py:119`

**期望值:**
- HTTP 状态码: `200`

**实际值:**
- HTTP 状态码: `400`

**一句话:** 多附件请求（包含URL类型）被拒绝，返回400错误

---

## 失败测试 #3: test_concurrent_session_conflict

**测试名称:** `test_concurrent_session_conflict`
**文件位置:** `tests/test_api_endpoints.py:240`

**期望值:**
- HTTP 状态码: `409` (Conflict)

**实际值:**
- HTTP 状态码: `200`

**一句话:** 同一session并发请求应返回409冲突，实际返回200成功接受

---

## 失败测试 #4: test_cancel_no_running_chat

**测试名称:** `test_cancel_no_running_chat`
**文件位置:** `tests/test_api_endpoints.py:314`

**期望值:**
- `data["status"] == "no_running_chat"`

**实际值:**
- `data["status"] == "success"`

**一句话:** 无活跃对话时取消应提示 `no_running_chat`，实际返回 `success`

---

## 失败测试 #5: test_get_running_status

**测试名称:** `test_get_running_status`
**文件位置:** `tests/test_api_endpoints.py:342`

**期望值:**
- `data["status"] == "success"`
- `data["chat"]` 包含活跃对话信息

**实际值:**
- `data["status"] == "idle"`
- `data["chat"] == null`

**一句话:** 活跃对话状态查询应返回对话信息，实际返回 idle 和 null

---

## 失败测试 #6: test_get_chat_detail

**测试名称:** `test_get_chat_detail`
**文件位置:** `tests/test_api_endpoints.py:403`

**期望值:**
- `chat` 字典包含 `"ended_at"` 字段

**实际值:**
- `chat` 字典缺少 `"ended_at"` 字段

**实际返回的chat字段:**
```json
{
  "attachments": [],
  "caller": "",
  "chat_id": "chat_20260419160933586",
  "duration": 0,
  "instruction": "帮我写一个函数",
  "result": "...",
  "round": 1,
  "started_at": "2026-04-19T...",
  "status": "completed"
}
```

**一句话:** ChatRecord 缺少 `ended_at` 字段

---

## 失败测试 #7: test_url_attachment

**测试名称:** `test_url_attachment`
**文件位置:** `tests/test_attachments.py:60`

**期望值:**
- HTTP 状态码: `200`

**实际值:**
- HTTP 状态码: `400`

**一句话:** URL类型附件应被接受，实际返回400拒绝

---

## 失败测试 #8: test_409_error_format

**测试名称:** `test_409_error_format`
**文件位置:** `tests/test_errors.py:70`

**期望值:**
- HTTP 状态码: `409`

**实际值:**
- HTTP 状态码: `200`

**一句话:** 同 test_concurrent_session_conflict，并发请求未返回409

---

## 失败测试 #9: test_error_contains_session_id_when_relevant

**测试名称:** `test_error_contains_session_id_when_relevant`
**文件位置:** `tests/test_errors.py:311`

**期望值:**
- 返回有效JSON响应（包含session_id）

**实际值:**
- 返回SSE流内容，无法解析为JSON
- `JSONDecodeError: Expecting value: line 1 column 1`

**一句话:** 并发错误时应返回JSON错误响应，实际返回SSE流

---

## 失败测试 #10: test_status_cancelled

**测试名称:** `test_status_cancelled`
**文件位置:** `tests/test_memory.py:435`

**期望值:**
- `messages[0]["status"] == "cancelled"`

**实际值:**
- `messages[0]["status"] == "completed"`

**一句话:** 取消后Memory保存的状态应为 `cancelled`，实际保存为 `completed`

---

## 失败测试 #11: test_register_active_chat

**测试名称:** `test_register_active_chat`
**文件位置:** `tests/test_runtime_state.py:39`

**期望值:**
- `data["chat"] is not None`

**实际值:**
- `data["chat"] == None`

**一句话:** GetActiveChat 应返回活跃对话信息，实际返回 null

---

## 失败测试 #12: test_is_running_check

**测试名称:** `test_is_running_check`
**文件位置:** `tests/test_runtime_state.py:66`

**期望值:**
- `status1.json()["chat"]["status"] == "running"`

**实际值:**
- `TypeError: 'NoneType' object is not subscriptable` (chat为null)

**一句话:** 同 test_register_active_chat，chat返回null导致无法访问status

---

## 失败测试 #13: test_cancelled_completed_event

**测试名称:** `test_cancelled_completed_event`
**文件位置:** `tests/test_sse_events.py:338`

**期望值:**
- completed事件 `status == "cancelled"`

**实际值:**
- completed事件 `status == "success"`

**completed事件内容:**
```json
{
  "status": "success",
  "duration": "0s",
  "round": 1
}
```

**一句话:** SSE completed事件在取消时应返回 `cancelled`，实际返回 `success`

---

## 失败测试 #14: test_cleanup_preserves_active_sessions

**测试名称:** `test_cleanup_preserves_active_sessions`
**文件位置:** `tests/test_supplementary.py:592`

**期望值:**
- `status.json()["chat"] is not None`

**实际值:**
- `status.json()["chat"] == None`

**一句话:** 同 test_register_active_chat，GetActiveChat返回null

---

## 失败测试 #15: test_cancel_interrupts_llm_call

**测试名称:** `test_cancel_interrupts_llm_call`
**文件位置:** `tests/test_supplementary.py:815`

**期望值:**
- completed事件 `status == "cancelled"`

**实际值:**
- completed事件 `status == "success"`

**一句话:** 取消中断LLM时应返回 `cancelled` 状态，实际返回 `success`

---

## 失败测试 #16: test_cancel_sse_pushes_event

**测试名称:** `test_cancel_sse_pushes_event`
**文件位置:** `tests/test_supplementary.py:865`

**期望值:**
- completed事件 `status == "cancelled"`

**实际值:**
- completed事件 `status == "success"`

**一句话:** 同 test_cancelled_completed_event

---

## 失败测试 #17: test_reasoning_step_emitted

**测试名称:** `test_reasoning_step_emitted`
**文件位置:** `tests/test_supplementary.py:890`

**期望值:**
- `len(step_starts) > 0` (应有reasoning步骤事件)

**实际值:**
- `len(step_starts) == 0` (没有任何step_start事件)

**一句话:** ReAct执行应发送 reasoning 步骤事件，实际没有发送

---

# Mock LLM 配置问题（6个）

以下6个测试失败原因是 **Mock LLM配置**，不是程序Bug：

| 测试名 | 文件位置 | 说明 |
|-------|---------|------|
| test_real_llm_code_generation | test_real_llm.py:66 | Mock返回固定消息 |
| test_real_llm_json_output | test_real_llm.py:93 | Mock返回固定消息 |
| test_real_llm_two_round_conversation | test_real_llm.py:142 | Mock返回固定消息 |
| test_real_llm_analysis_task | test_real_llm.py:302 | Mock返回固定消息 |
| test_real_llm_translation_task | test_real_llm.py:326 | Mock返回固定消息 |
| test_real_llm_math_problem | test_real_llm.py:347 | Mock返回固定消息 |

---

# 总结

## Bug修复状态

| 轮次 | 通过 | 失败 | 通过率 | Bug修复数 |
|------|------|------|--------|-----------|
| 第十一轮 | 239 | 23 | 88.5% | 0 |
| **第十二轮** | 239 | 23 | 88.5% | **0** |

**结论: 程序员没有修复任何Bug！所有17个程序Bug仍然存在。**

---

## 建议

请程序员：
1. 检查代码是否实际修改
2. 确认修改后是否重新编译/部署
3. 确认服务是否重启
4. 参考 `bug-fix-list-17-bugs-2026-04-19.md` 进行修复

---

**报告生成日期:** 2026-04-19