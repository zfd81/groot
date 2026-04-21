# Groot API 测试用例

**版本:** 1.0.0
**日期:** 2026-04-18
**对应设计:** docs/superpowers/specs/2026-04-18-groot-agent-design.md

---

## 设计变化说明

根据 2026-04-18 新版设计文档，相比旧版有以下变化：

### 删除的功能（对应测试已移除）

| 删除项 | 旧版配置 | 说明 |
|--------|----------|------|
| BoltDB 存储 | `storage.engine: boltdb` | 改用文件系统存储 |
| 限流配置 | `performance.rate_limit` | 删除 max_concurrent_tasks 等 |
| 并发调用限制 | `performance.llm/mcp` | 删除 LLM/MCP 并发限制 |
| 存储引擎 | `storage` 配置节 | 删除整个存储配置 |

### 新增的功能（新增测试）

| 新增项 | 说明 |
|--------|------|
| RuntimeState 模块 | sync.Map 内存管理，活跃状态追踪 |
| chats/{chat_id}.json | 详细执行记录存储 |
| SaveChatRecord | Memory 新增方法 |
| GetChatRecord | Memory 新增方法 |
| step_id 格式 | `{YYYYMMDD}-{HHMMSSmmm}-{random6}` |

### 格式变化（测试已更新）

| 变化项 | 旧版 | 新版 |
|--------|------|------|
| session_id 格式 | `sess_{timestamp}_{random}` | `{timestamp}_{random}`（无前缀） |
| 目录名 | `sess_xxx/` | `{session_id}/` |
| 新增 chats 目录 | 无 | `chats/{chat_id}.json` |
| history.json 字段 | `user_content` | `instruction` |
| history.json 字段 | `assistant_content` | `result` |
| history.json 字段 | `user_attachments` | `attachments` |
| history.json 字段 | `assistant_attachments` | `result_attachments` |
| 新增字段 | - | `chat_id`, `status`, `duration`, `steps_count`, `error` |
| 日志事件 | `task_completed` | `chat_completed` |

---

## 一、测试环境配置

### 1.1 测试前准备

```bash
# 设置环境变量
export GROOT_HOME=/tmp/groot_test
export GROOT_API_KEY=test-api-key-2026
export OPENAI_API_KEY=sk-test-key

# 创建测试目录
mkdir -p $GROOT_HOME
mkdir -p $GROOT_HOME/skills
mkdir -p $GROOT_HOME/mcp
mkdir -p $GROOT_HOME/memory
mkdir -p $GROOT_HOME/logs

# 启动服务（测试模式）
groot -H $GROOT_HOME -p 8080
```

### 1.2 测试配置文件

```yaml
# $GROOT_HOME/config.yaml
agent:
  name: groot
  version: 1.0.0

server:
  host: 0.0.0.0
  port: 8080

llm:
  default_model: mock-model
  models:
    mock-model:
      base_url: http://localhost:8888/mock
      api_key: mock-key
      model: mock
      max_tokens: 4096
      temperature: 0.7

security:
  auth:
    enabled: true
    type: api_key
    api_key:
      header_name: X-API-Key
      keys:
        - name: test_client
          key: test-api-key-2026
          permissions: all

memory:
  directory: memory
  retention_days: 1
  cleanup_schedule: "02:00"

logging:
  level: debug
  format: json
  output: [stdout]
```

---

## 二、API 测试用例

### 2.1 POST /chat - 新会话（无附件）

**测试编号:** TC-001
**测试名称:** 新会话基本对话
**优先级:** P0

**前置条件:**
- 服务已启动
- 无活跃会话

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: test-api-key-2026

{
  "instruction": "帮我写一个Python快速排序函数"
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| Content-Type | text/event-stream |
| X-Session-ID | 格式：`{YYYYMMDDHHMMSSmmm}_{random4}` |
| X-Chat-ID | 格式：`chat_{YYYYMMDDHHMMSSmmm}` |
| SSE事件顺序 | intent → step_start → ... → step_end → completed |
| intent事件 | `{"timestamp":"..."}` |
| completed事件.status | success |
| completed事件.round | 1 |
| completed事件.duration | 格式如 "45s" |

**验证点:**
1. Session ID 格式正确（无 sess_ 前缀）
2. Chat ID 格式正确（chat_ 前缀）
3. SSE 事件顺序正确
4. completed 包含 round 字段且值为 1

---

### 2.2 POST /chat - 新会话（带附件）

**测试编号:** TC-002
**测试名称:** 新会话带附件对话
**优先级:** P0

**前置条件:**
- 服务已启动
- 准备测试文件（PDF/CSV）

**测试数据准备:**

```bash
# 创建测试文件
echo "name,age,city\nAlice,25,Beijing\nBob,30,Shanghai" > /tmp/test_data.csv

# Base64编码
BASE64_DATA=$(base64 -w 0 /tmp/test_data.csv)
```

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: test-api-key-2026

{
  "instruction": "帮我分析这个CSV数据",
  "attachments": [
    {
      "type": "file",
      "name": "test_data.csv",
      "content": "${BASE64_DATA}"
    }
  ]
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| X-Session-ID | 新生成的session_id |
| SSE事件 | 包含 step_start.type=file_read |
| 附件存储路径 | `$GROOT_HOME/memory/{sid}/attachments/test_data.csv` |
| completed事件.result | 包含分析结果 |

**验证点:**
1. 附件正确解码并保存
2. 附件路径格式正确（无 sess_ 前缀）
3. SSE 包含文件读取步骤

---

### 2.3 POST /chat - 多附件请求

**测试编号:** TC-003
**测试名称:** 多附件对话
**优先级:** P1

**前置条件:**
- 服务已启动
- 准备多个测试文件

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: test-api-key-2026

{
  "instruction": "对比分析这两个文件",
  "attachments": [
    {"type": "file", "name": "file1.csv", "content": "base64..."},
    {"type": "file", "name": "file2.csv", "content": "base64..."},
    {"type": "url", "name": "external.pdf", "url": "https://example.com/doc.pdf"}
  ]
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| 附件数量 | 3个文件信息正确传递 |
| 附件目录 | attachments/ 包含 file1.csv, file2.csv |

---

### 2.4 POST /chat - 带prompt参数

**测试编号:** TC-004
**测试名称:** 自定义系统提示词
**优先级:** P1

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: test-api-key-2026

{
  "instruction": "分析这份报告",
  "prompt": "你是一个财务分析师，重点关注利润增长率和潜在风险点。输出JSON格式。",
  "attachments": [{"type": "file", "name": "report.pdf", "content": "base64..."}]
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| completed.result | JSON格式，包含财务分析字段 |

---

### 2.5 POST /chat - 继续会话（多轮对话）

**测试编号:** TC-005
**测试名称:** 多轮对话继续会话
**优先级:** P0

**前置条件:**
- 已完成 TC-001，获得 session_id

**测试步骤:**

```http
# 第一轮：获得session_id
POST /chat HTTP/1.1
...得到 session_id: 20260418103000523_a1b2

# 第二轮：使用相同session_id继续
POST /chat HTTP/1.1
Host: localhost:8080
X-Session-ID: 20260418103000523_a1b2
Content-Type: application/json
X-API-Key: test-api-key-2026

{
  "instruction": "根据刚才的函数，添加注释和类型提示"
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| X-Session-ID | 与请求header相同（20260418103000523_a1b2） |
| X-Chat-ID | 新的chat_id（不同于第一轮） |
| completed.round | 2 |
| SSE事件 | Agent使用历史上下文生成回复 |

**验证点:**
1. Session ID 格式验证（无 sess_ 前缀）
2. round 字段递增
3. history.json 中 messages 增加

---

### 2.6 POST /chat - 会话不存在自动创建

**测试编号:** TC-006
**测试名称:** 无效session_id自动创建新会话
**优先级:** P1

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
X-Session-ID: invalid_session_id_12345
Content-Type: application/json
X-API-Key: test-api-key-2026

{
  "instruction": "测试指令"
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| X-Session-ID | 新生成的session_id（非请求中的invalid值） |
| completed.round | 1 |

---

### 2.7 POST /chat - 并发冲突（409）

**测试编号:** TC-007
**测试名称:** 会话并发执行冲突
**优先级:** P0

**前置条件:**
- 会话 A 正在执行（长耗时任务）

**测试步骤:**

```bash
# 同时发起两个请求，使用相同session_id
# 请求1：长任务（在执行中）
curl -X POST ... -H "X-Session-ID: 20260418103000523_a1b2" ...

# 请求2：同时发起（应返回409）
curl -X POST ... -H "X-Session-ID: 20260418103000523_a1b2" ...
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 409 |
| response.status | chat_limit_exceeded |
| response.message | "该会话已有对话正在执行..." |

---

### 2.8 POST /chat - 附件校验失败

**测试编号:** TC-008
**测试名称:** 附件大小超限
**优先级:** P1

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: test-api-key-2026

{
  "instruction": "分析大文件",
  "attachments": [
    {"type": "file", "name": "huge.pdf", "content": "base64...超过50MB"}
  ]
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 400 |
| error.code | attachment_size_exceeded |
| error.message | 包含大小限制说明 |

---

**测试编号:** TC-009
**测试名称:** 附件类型不允许
**优先级:** P1

**测试步骤:**

```http
POST /chat HTTP/1.1
...
{
  "instruction": "执行脚本",
  "attachments": [
    {"type": "file", "name": "malware.exe", "content": "base64..."}
  ]
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 400 |
| error.code | attachment_type_not_allowed |

---

**测试编号:** TC-010
**测试名称:** 附件数量超限
**优先级:** P1

**测试步骤:**

```http
POST /chat HTTP/1.1
...
{
  "instruction": "分析文件",
  "attachments": [
    # 超过10个附件
    {"type": "file", "name": "file1.pdf", "content": "..."},
    {"type": "file", "name": "file2.pdf", "content": "..."},
    ... # 共11个
  ]
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 400 |
| error.code | attachment_count_exceeded |

---

### 2.9 POST /chat - 认证失败

**测试编号:** TC-011
**测试名称:** 无API Key
**优先级:** P0

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
# 缺少 X-API-Key

{
  "instruction": "测试指令"
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 401 |
| response.status | unauthorized |
| response.message | "API Key 无效或缺失" |

---

**测试编号:** TC-012
**测试名称:** 无效API Key
**优先级:** P0

**测试步骤:**

```http
POST /chat HTTP/1.1
Host: localhost:8080
Content-Type: application/json
X-API-Key: invalid-key-12345

{
  "instruction": "测试指令"
}
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 401 |
| response.status | unauthorized |

---

### 2.10 DELETE /chat/{sid} - 取消对话

**测试编号:** TC-013
**测试名称:** 取消正在执行的对话
**优先级:** P0

**前置条件:**
- 会话正在执行（长任务）

**测试步骤:**

```http
DELETE /chat/20260418103000523_a1b2 HTTP/1.1
Host: localhost:8080
X-API-Key: test-api-key-2026
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| response.status | success |
| response.session_id | 20260418103000523_a1b2 |
| SSE completed事件 | {"status":"cancelled","message":"用户主动取消"} |

---

**测试编号:** TC-014
**测试名称:** 取消无执行会话
**优先级:** P1

**测试步骤:**

```http
DELETE /chat/20260418103000523_a1b2 HTTP/1.1
# 该会话当前无对话执行
```

**预期结果:**

| 验证项 | 预期值 |
|--------|--------|
| HTTP状态码 | 200 |
| response.status | no_running_chat |

---

### 2.11 GET /chat/status/{sid}

**测试编号:** TC-015
**测试名称:** 查询执行中的对话状态
**优先级:** P0

**前置条件:**
- 会话正在执行

**测试步骤:**

```http
GET /chat/status/20260418103000523_a1b2 HTTP/1.1
Host: localhost:8080
X-API-Key: test-api-key-2026
```

**预期结果:**

```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "chat": {
    "chat_id": "chat_20260418103000523",
    "round": 1,
    "status": "running",
    "progress": {
      "current_step": 2,
      "steps_completed": 1,
      "percentage": 50
    },
    "started_at": "2026-04-18T10:30:00Z",
    "elapsed_time": "15s"
  }
}
```

---

**测试编号:** TC-016
**测试名称:** 查询无执行会话状态
**优先级:** P1

**预期结果:**

```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "chat": null
}
```

---

### 2.12 GET /chat/{sid} - 查询对话详情

**测试编号:** TC-017
**测试名称:** 查询最近对话详情（完整步骤）
**优先级:** P0

**前置条件:**
- 会话有完成的对话记录

**测试步骤:**

```http
GET /chat/20260418103000523_a1b2 HTTP/1.1
Host: localhost:8080
X-API-Key: test-api-key-2026
```

**预期结果:**

```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "chat": {
    "chat_id": "chat_20260418103000523",
    "round": 1,
    "instruction": "用户指令内容",
    "attachments": ["data.csv"],
    "result": {"summary": "执行结果..."},
    "status": "completed",
    "started_at": "2026-04-18T10:30:00Z",
    "ended_at": "2026-04-18T10:30:45Z",
    "duration": 45,
    "steps": [
      {
        "step_id": "20260418-103000000-a1b2c3",
        "type": "skill",
        "name": "pdf_analyzer",
        "start_time": "2026-04-18T10:30:00Z",
        "end_time": "2026-04-18T10:30:30Z",
        "status": "success",
        "nesting_level": 0
      }
    ]
  }
}
```

**验证点:**
1. step_id 格式正确：`{YYYYMMDD}-{HHMMSSmmm}-{random6}`
2. nesting_level 存在
3. 字段名使用 instruction/result（非旧版 user_content/assistant_content）

---

### 2.13 GET /sess/{sid} - 查询会话详情

**测试编号:** TC-018
**测试名称:** 查询会话完整历史
**优先级:** P0

**前置条件:**
- 会话有多个轮次（至少2轮）

**测试步骤:**

```http
GET /sess/20260418103000523_a1b2 HTTP/1.1
Host: localhost:8080
X-API-Key: test-api-key-2026
```

**预期结果:**

```json
{
  "status": "success",
  "session_id": "20260418103000523_a1b2",
  "session": {
    "created_at": "2026-04-18T10:00:00Z",
    "round_count": 4,
    "path": "/tmp/groot_test/memory/20260418103000523_a1b2"
  },
  "history": {
    "messages": [
      {
        "round": 1,
        "chat_id": "chat_20260418100000523",
        "timestamp": "2026-04-18T10:00:00Z",
        "instruction": "帮我分析这个数据文件",
        "attachments": ["data.csv"],
        "result": "好的，分析结果如下...",
        "result_attachments": [],
        "status": "completed",
        "duration": 45,
        "steps_count": 3,
        "error": null
      }
    ]
  }
}
```

**验证点:**
1. session_id 格式无 sess_ 前缀
2. path 格式无 sess_ 前缀
3. messages 字段使用新名称：instruction/result/attachments/result_attachments
4. 包含 chat_id 字段
5. 包含 status/duration/steps_count/error 字段

---

### 2.14 GET /sess/history - 查询会话列表

**测试编号:** TC-019
**测试名称:** 查询会话列表（分页）
**优先级:** P0

**测试步骤:**

```http
GET /sess/history?limit=10&offset=0 HTTP/1.1
Host: localhost:8080
X-API-Key: test-api-key-2026
```

**预期结果:**

```json
{
  "status": "success",
  "total": 50,
  "limit": 10,
  "offset": 0,
  "sessions": [
    {
      "session_id": "20260418103000523_a1b2",
      "created_at": "2026-04-18T10:00:00Z",
      "round_count": 4,
      "last_active_at": "2026-04-18T10:30:00Z"
    }
  ]
}
```

**验证点:**
1. session_id 格式无 sess_ 前缀
2. 分页参数生效

---

### 2.15 GET /health - 健康检查

**测试编号:** TC-020
**测试名称:** 健康检查接口
**优先级:** P0

**测试步骤:**

```http
GET /health HTTP/1.1
Host: localhost:8080
```

**预期结果:**

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "checks": {
    "llm": {"status": "healthy", "model": "mock-model"},
    "mcp_servers": {"status": "healthy", "servers": []},
    "skills": {"status": "healthy", "count": 0},
    "memory": {"status": "healthy", "used_mb": 0}
  },
  "metrics": {
    "chats_running": 0,
    "success_rate": 1.0
  }
}
```

---

### 2.16 GET /skills

**测试编号:** TC-021
**测试名称:** 列出Skills
**优先级:** P1

**预期结果:**

```json
{
  "skills": [],
  "total": 0
}
```

---

### 2.17 GET /tools

**测试编号:** TC-022
**测试名称:** 列出MCP工具
**优先级:** P1

**预期结果:**

```json
{
  "tools": [
    {"name": "file_read", "description": "读取文件内容", "mcp": "file_operations"},
    {"name": "file_write", "description": "写入文件内容", "mcp": "file_operations"}
  ],
  "total": 2
}
```

---

## 三、SSE 事件测试

### 3.1 SSE 事件顺序验证

**测试编号:** TC-023
**测试名称:** SSE事件顺序正确性
**优先级:** P0

**预期事件顺序:**
```
intent → step_start → progress → step_end → ... → completed
```

**验证点:**
1. intent 必须是首个事件
2. completed 必须是最后一个事件
3. step_start 和 step_end 必须成对
4. progress 在 step_start 和 step_end 之间

---

### 3.2 step_start 事件字段验证

**测试编号:** TC-024
**测试名称:** step_start字段完整性
**优先级:** P0

**预期结构:**

```json
{
  "type": "skill",
  "name": "pdf_analyzer",
  "step_id": "20260418-103000000-a1b2c3",
  "timestamp": "2026-04-18T10:30:00Z",
  "nesting_level": 0,
  "params": null
}
```

**验证点:**
1. step_id 格式：`{YYYYMMDD}-{HHMMSSmmm}-{random6}`
2. type 可选值：skill / tool / llm
3. nesting_level 存在（默认0）
4. timestamp ISO格式

---

### 3.3 completed 事件字段验证

**测试编号:** TC-025
**测试名称:** completed字段完整性
**优先级:** P0

**预期结构:**

```json
{
  "status": "success",
  "timestamp": "2026-04-18T10:30:45Z",
  "duration": "45s",
  "round": 1,
  "result": {...}
}
```

**验证点:**
1. status 可选值：success / failed / cancelled
2. duration 格式正确（如 "45s", "1m30s"）
3. round 字段存在且为整数
4. timestamp ISO格式

---

## 四、Memory 模块测试

### 4.1 history.json 结构验证

**测试编号:** TC-026
**测试名称:** history.json文件结构
**优先级:** P0

**前置条件:**
- 完成至少一轮对话

**验证文件:**
`$GROOT_HOME/memory/{session_id}/history.json`

**预期结构:**

```json
{
  "session_id": "20260418103000523_a1b2",
  "created_at": "2026-04-18T10:00:00Z",
  "messages": [
    {
      "round": 1,
      "chat_id": "chat_20260418100000523",
      "timestamp": "2026-04-18T10:00:00Z",
      "instruction": "用户指令",
      "attachments": ["data.csv"],
      "result": "助手回复",
      "result_attachments": [],
      "status": "completed",
      "duration": 45,
      "steps_count": 3,
      "error": null
    }
  ]
}
```

**验证点:**
1. session_id 无 sess_ 前缀
2. 字段名使用新版：instruction/result/attachments/result_attachments
3. 包含 chat_id 字段
4. 包含 status/duration/steps_count/error 字段

---

### 4.2 chat记录文件验证

**测试编号:** TC-027
**测试名称:** chats/{chat_id}.json文件结构
**优先级:** P0

**验证文件:**
`$GROOT_HOME/memory/{session_id}/chats/chat_{timestamp}.json`

**预期结构:**

```json
{
  "chat_id": "chat_20260418100000523",
  "session_id": "20260418103000523_a1b2",
  "round": 1,
  "timestamp": "2026-04-18T10:00:00Z",
  "instruction": "用户指令",
  "attachments": ["report.pdf"],
  "result": "执行结果",
  "result_attachments": [],
  "status": "completed",
  "duration": 45,
  "caller": "test_client",
  "steps": [
    {
      "step_id": "20260418-100005000-a1b2c3",
      "type": "skill",
      "name": "pdf_analyzer",
      "start_time": "2026-04-18T10:00:05Z",
      "end_time": "2026-04-18T10:00:30Z",
      "status": "success",
      "nesting_level": 0
    }
  ],
  "error": null
}
```

**验证点:**
1. chat_id 格式正确
2. session_id 无 sess_ 前缀
3. steps 数组包含完整执行记录
4. step_id 格式正确

---

### 4.3 附件存储验证

**测试编号:** TC-028
**测试名称:** 附件文件存储
**优先级:** P0

**验证路径:**
`$GROOT_HOME/memory/{session_id}/attachments/{filename}`

**验证点:**
1. 文件正确保存
2. 文件名保留原始名（不添加前缀）
3. 同名文件覆盖

---

### 4.4 目录结构验证

**测试编号:** TC-029
**测试名称:** Memory目录结构完整性
**优先级:** P0

**预期结构:**

```
$GROOT_HOME/memory/
├── 20260418103000523_a1b2/        # 无 sess_ 前缀
│   ├── history.json
│   ├── attachments/
│   │   └── data.csv
│   └── chats/                     # 新增目录
│       └── chat_20260418100000523.json
└── 20260418103500123_b2c3/
    ├── history.json
    └── ...
```

**验证点:**
1. 目录名无 sess_ 前缀
2. chats/ 子目录存在
3. history.json 存在
4. attachments/ 目录（有附件时）

---

## 五、RuntimeState 测试

### 5.1 并发控制验证

**测试编号:** TC-030
**测试名称:** 同一会话并发限制
**优先级:** P0

**测试步骤:**
1. 发起长耗时对话（session_id: A）
2. 同时发起第二个对话（使用相同session_id: A）
3. 第二个请求应返回 409

**验证点:**
1. RuntimeState.IsRunning(session_id) 返回 true
2. 409 响应正确

---

### 5.2 状态生命周期验证

**测试编号:** TC-031
**测试名称:** 状态注册与清理
**优先级:** P0

**测试步骤:**
1. POST /chat → RuntimeState.Register(session_id, chat_id)
2. 执行中 → RuntimeState.UpdateProgress()
3. 完成 → RuntimeState.Complete(session_id) → 移除活跃状态
4. GET /chat/status/{sid} → chat: null

---

## 六、错误处理测试

### 6.1 错误响应格式

**测试编号:** TC-032
**测试名称:** 错误响应统一格式
**优先级:** P0

**预期格式:**

```json
{
  "status": "error_code",
  "message": "错误描述",
  "session_id": "xxx"  // 可选
}
```

---

### 6.2 错误码完整列表

| HTTP状态 | 错误码 | 说明 |
|----------|--------|------|
| 400 | invalid_request | 请求参数错误 |
| 400 | attachment_count_exceeded | 附件数量超限 |
| 400 | attachment_type_not_allowed | 附件类型不允许 |
| 400 | attachment_size_exceeded | 附件大小超限 |
| 400 | attachment_total_size_exceeded | 附件总大小超限 |
| 400 | attachment_decode_error | Base64解码失败 |
| 401 | unauthorized | API Key无效 |
| 403 | forbidden | 权限不足 |
| 409 | chat_limit_exceeded | 会话已有对话执行中 |
| 409 | session_not_found | 会话不存在 |
| 500 | config_error | 配置错误 |
| 500 | llm_connection_error | LLM连接失败 |

---

## 七、边界测试

### 7.1 极限测试

**测试编号:** TC-033
**测试名称:** 最大附件数量边界
**优先级:** P2

**测试数据:** 附件数量 = 10（配置max_count）

---

**测试编号:** TC-034
**测试名称:** 最大附件大小边界
**优先级:** P2

**测试数据:** 单个附件 = 50MB

---

### 7.2 特殊字符测试

**测试编号:** TC-035
**测试名称:** 文件名安全处理
**优先级:** P1

**测试数据:**
- 文件名包含 `/` → 替换为 `_`
- 文件名包含 `\` → 替换为 `_`
- 文件名包含 `..` → 替换为 `_`

---

## 八、补充测试（覆盖设计文档全部功能）

### 8.1 LLM/MCP 错误处理测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-LLM-001 | llm_connection_error 错误码 | LLM 连接失败错误处理 |
| TC-LLM-002 | llm_rate_limited 错误码 | LLM API 限流错误处理 |
| TC-LLM-003 | LLM 连接失败重试（3次，间隔2s）| 重试策略验证 |
| TC-LLM-004 | LLM Rate Limit 重试间隔（5s）| 限流重试间隔验证 |

### 8.2 MCP 工具错误处理测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-MCP-001 | tool_call_error 错误码 | MCP 工具调用失败 |
| TC-MCP-002 | MCP 工具失败重试（2次，间隔1s）| MCP 重试策略 |

### 8.3 MCP 连接类型测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-MCP-TYPE-001 | stdio 类型 MCP 配置 | 本地命令行工具配置 |
| TC-MCP-TYPE-002 | sse 类型 MCP 配置 | 远程 SSE 服务配置 |
| TC-MCP-TYPE-003 | streamable_http 类型 MCP 配置 | Streamable HTTP 配置 |
| TC-MCP-TYPE-004 | MCP headers 环境变量引用 | ${VAR_NAME} 格式引用 |

### 8.4 Skills 依赖测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-SKILL-DEP-001 | Skill dependencies 递归调用 | 嵌套 Skill 调用验证 |

### 8.5 http_request 内置工具限制测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-HTTP-001 | http_request 30秒超时 | 超时限制验证 |
| TC-HTTP-002 | http_request 最大响应 10MB | 响应大小限制 |

### 8.6 code_execution 安全限制测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-CODE-001 | code_execution 默认禁用 | 高风险工具禁用验证 |
| TC-CODE-002 | code_execution 沙箱禁止网络 | 沙箱网络隔离 |

### 8.7 prompt 参数验证测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-PROMPT-001 | prompt 参数正常接受 | 自定义系统提示词 |
| TC-PROMPT-002 | prompt 为空允许 | 空 prompt 处理 |

### 8.8 Health 详细检查测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-HEALTH-001 | LLM 连接就绪检查 | 就绪探针 |
| TC-HEALTH-002 | MCP 服务连接就绪检查 | MCP 健康检查 |
| TC-HEALTH-003 | Skills 加载完成检查 | Skills 就绪验证 |
| TC-HEALTH-004 | Memory 使用检查 | 内存健康状态 |
| TC-HEALTH-005 | uptime 字段 | 运行时间显示 |
| TC-HEALTH-006 | version 字段 | 版本信息显示 |

### 8.9 Memory 清理逻辑测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-MEM-CLN-001 | 会话保留天数配置 | retention_days 配置验证 |
| TC-MEM-CLN-002 | 清理过期会话 | 定时清理验证 |
| TC-MEM-CLN-003 | 清理保留活跃会话 | 活跃会话不被清理 |

### 8.10 优雅关闭测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-SHUT-001 | 关闭时等待运行中的对话 | 优雅关闭等待 |
| TC-SHUT-002 | 关闭超时30秒 | 关闭超时限制 |

### 8.11 配置热更新边界测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-CFG-HOT-001 | LLM 配置不支持热更新 | 需重启服务 |
| TC-CFG-HOT-002 | Server 配置不支持热更新 | 需重启服务 |
| TC-CFG-HOT-003 | Security 配置不支持热更新 | 需重启服务 |
| TC-CFG-HOT-004 | Skills 配置支持热更新 | 动态加载验证 |
| TC-CFG-HOT-005 | MCP 配置支持热更新 | 动态加载验证 |

### 8.12 LLM 多模型配置测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-LLM-CFG-001 | default_model 配置 | 默认模型 |
| TC-LLM-CFG-002 | models 配置列表 | 多模型配置 |
| TC-LLM-CFG-003 | api_key 环境变量引用 | 环境变量格式 |
| TC-LLM-CFG-004 | X-Model-Name 指定有效模型 | Header 指定模型 |
| TC-LLM-CFG-005 | X-Model-Name 指定不存在模型 | 返回 400 invalid_model |
| TC-LLM-CFG-006 | X-Model-Name 为空 | 使用默认模型 |
| TC-LLM-CFG-007 | default_model 不存在于 models | 启动时验证失败退出 |
| TC-LLM-CFG-008 | LLM 服务地址错误 | SSE 返回 execution_error |
| TC-LLM-CFG-009 | LLM 服务未启动 | SSE 返回 connection refused |
| TC-LLM-CFG-010 | 同会话不同模型切换 | 多轮对话使用不同模型 |

### 8.13 配置验证测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-CFG-VAL-001 | 启动时验证 default_model 存在 | 配置验证 |
| TC-CFG-VAL-002 | 启动时验证 models 不为空 | 配置验证 |
| TC-CFG-VAL-003 | YAML 解析不合并默认配置 | 用户配置完全控制 |

### 8.14 SSE 错误事件测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-SSE-ERR-001 | LLM 连接失败返回错误事件 | SSE error 事件 |
| TC-SSE-ERR-002 | 错误事件格式验证 | {"code":"execution_error"} |
| TC-SSE-ERR-003 | 错误后发送 [DONE] | SSE 结束标记 |

### 8.15 权限边界测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-PERM-001 | chat 权限仅允许对话 | 权限范围限制 |
| TC-PERM-002 | status 权限仅允许状态查询 | 权限范围限制 |
| TC-PERM-003 | all 权限可访问所有 API | 全权限验证 |

### 8.16 取消机制详细测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-CANCEL-001 | 取消中断 LLM 调用 | LLM 调用中断 |
| TC-CANCEL-002 | 取消中断 MCP 工具调用 | MCP 调用中断 |
| TC-CANCEL-003 | 取消推送 SSE cancelled 事件 | SSE 取消事件 |

### 8.17 ReAct 执行详细测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-REACT-001 | Reasoning 步骤事件 | 思考步骤验证 |
| TC-REACT-002 | Acting 工具调用步骤 | 工具调用步骤 |
| TC-REACT-003 | Observation 结果更新 | 结果观察验证 |

### 8.18 会话处理详细测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-SESS-001 | 新会话历史为空 | 新会话状态验证 |
| TC-SESS-002 | 继续会话加载历史 | 历史上下文加载 |
| TC-SESS-003 | 无效 session_id 创建新会话 | 自动创建验证 |

### 8.19 Health metrics 测试

| 测试编号 | 测试名称 | 说明 |
|---------|---------|------|
| TC-METRICS-001 | chats_running 指标 | 运行对话计数 |
| TC-METRICS-002 | success_rate 指标 | 成功率统计 |

---

## 九、测试覆盖率统计（完整版）

| 模块 | 测试用例数 | P0 | P1 | P2 |
|------|-----------|----|----|----|
| POST /chat | 12 | 5 | 5 | 2 |
| DELETE /chat | 2 | 1 | 1 | 0 |
| GET /chat/status | 2 | 1 | 1 | 0 |
| GET /chat/{sid} | 1 | 1 | 0 | 0 |
| GET /sess/{sid} | 1 | 1 | 0 | 0 |
| GET /sess/history | 1 | 1 | 0 | 0 |
| GET /health | 1 | 1 | 0 | 0 |
| SSE事件 | 3 | 3 | 0 | 0 |
| Memory模块 | 4 | 4 | 0 | 0 |
| RuntimeState | 2 | 2 | 0 | 0 |
| 错误处理 | 2 | 1 | 1 | 0 |
| 边界测试 | 3 | 0 | 1 | 2 |
| LLM/MCP 错误处理 | 4 | 2 | 2 | 0 |
| MCP 连接类型 | 4 | 1 | 3 | 0 |
| Skills 依赖 | 1 | 1 | 0 | 0 |
| http_request 限制 | 2 | 1 | 1 | 0 |
| code_execution 限制 | 2 | 1 | 1 | 0 |
| prompt 参数验证 | 2 | 1 | 1 | 0 |
| Health 详细检查 | 6 | 3 | 3 | 0 |
| Memory 清理逻辑 | 3 | 2 | 1 | 0 |
| 优雅关闭 | 2 | 1 | 1 | 0 |
| 配置热更新边界 | 5 | 2 | 3 | 0 |
| LLM 多模型配置 | 10 | 4 | 4 | 2 |
| 配置验证 | 3 | 3 | 0 | 0 |
| SSE 错误事件 | 3 | 3 | 0 | 0 |
| 权限边界 | 3 | 1 | 2 | 0 |
| 取消机制详细 | 3 | 2 | 1 | 0 |
| ReAct 执行详细 | 3 | 2 | 1 | 0 |
| 会话处理详细 | 3 | 2 | 1 | 0 |
| Health metrics | 2 | 1 | 1 | 0 |
| **总计** | **83** | **46** | **28** | **9** |

---

## 十、测试执行顺序建议（完整版）

**执行顺序（按依赖关系）：**

1. 基础接口测试（TC-001, TC-002）
2. 认证测试（TC-011, TC-012）
3. 多轮对话测试（TC-005）
4. 附件校验测试（TC-008, TC-009, TC-010）
5. 状态查询测试（TC-015, TC-017, TC-018）
6. 并发测试（TC-007）
7. 取消测试（TC-013）
8. SSE事件验证（TC-023, TC-024, TC-025）
9. Memory结构验证（TC-026, TC-027, TC-028）
10. RuntimeState 测试（TC-030, TC-031）
11. 边界测试（TC-033, TC-034, TC-035）
12. 补充测试（第八章全部）