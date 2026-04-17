# Groot Agent 测试用例规范

**版本:** 1.0.0
**日期:** 2026-04-17
**状态:** 测试用例完整

---

## 一、测试环境配置

### 1.1 测试前提条件

| 条件 | 说明 |
|------|------|
| LLM 服务 | 需要可用的 LLM API 服务 |
| Groot 服务 | 已编译并可正常启动 |
| 测试目录 | 创建测试专用工作目录 |

### 1.2 测试配置文件

```bash
# 测试环境变量
export GROOT_TEST_HOME=~/.groot-test
export GROOT_TEST_PORT=8080
export GROOT_LLM_BASE_URL="http://127.0.0.1:8230/v1"
export GROOT_LLM_API_KEY="bonc1q2w3e"
export GROOT_LLM_MODEL="Qwen3.5-122B-A10B-6bit"
```

### 1.3 测试启动脚本

```bash
# 启动测试服务
./groot -H $GROOT_TEST_HOME -p $GROOT_TEST_PORT
```

---

## 二、API 端点测试用例

### 2.1 POST /task/execute

#### TC-API-001: 基本任务执行

**测试目的:** 验证基本的任务执行功能

**前置条件:**
- 服务已启动
- LLM 配置正确

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "你好，请简短介绍一下你自己"}'
```

**预期结果:**
- HTTP Header 返回 `X-Task-ID`
- Content-Type 为 `text/event-stream`
- SSE 流返回 `intent` 事件
- SSE 流返回多个 `progress` 事件
- SSE 流返回 `completed` 事件，包含 `result` 字段
- `result` 字段不为空

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| X-Task-ID Header | 存在，格式为 task-{YYYYMMDD}-{HHMMSSmmm}-{random4} |
| Content-Type | text/event-stream |
| intent 事件 | 存在，包含 timestamp |
| progress 事件 | 至少一个，包含 message |
| completed 事件 | status=success，result 不为空 |

---

#### TC-API-002: 带 prompt 的任务执行

**测试目的:** 验证自定义 prompt 参数

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{
    "instruction": "帮我写一个排序算法",
    "prompt": "你是一个 Python 专家，请用 Python 语言回答"
  }'
```

**预期结果:**
- 任务正常执行
- 响应内容体现 prompt 设定的角色

---

#### TC-API-003: 空指令请求

**测试目的:** 验证参数校验

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": ""}'
```

**预期结果:**
- HTTP 状态码 400
- 返回错误信息

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| HTTP 状态码 | 400 |
| status | invalid_request |
| message | instruction 字段不能为空 |

---

#### TC-API-004: 无效 JSON 请求

**测试目的:** 验证请求体解析

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d 'invalid json'
```

**预期结果:**
- HTTP 状态码 400
- 返回错误信息

---

#### TC-API-005: 带 Base64 附件的请求

**测试目的:** 验证附件处理

**测试步骤:**
```bash
# 创建测试文件并编码
echo "test content" > /tmp/test.txt
CONTENT=$(base64 /tmp/test.txt)

curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{
    "instruction": "帮我分析这个文件的内容",
    "attachments": [
      {"type": "file", "name": "test.txt", "content": "'$CONTENT'"}
    ]
  }'
```

**预期结果:**
- 任务正常执行
- Agent 能识别附件信息

---

#### TC-API-006: 任务执行后存储验证

**测试目的:** 验证任务持久化

**测试步骤:**
1. 执行任务，记录 task_id
2. 查询任务详情 API
3. 验证存储记录

**预期结果:**
- 任务记录正确存储到 BoltDB
- 可通过 API 查询到完整记录

---

### 2.2 DELETE /task/{task_id}

#### TC-API-007: 取消正在执行的任务

**测试目的:** 验证任务取消功能

**前置条件:**
- 有正在执行的任务

**测试步骤:**
```bash
# 先启动一个长任务
TASK_ID=$(curl -s -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我详细分析一下人工智能的发展历史"}' \
  | grep -o 'X-Task-ID: task-[0-9-]*' | cut -d' ' -f2)

# 立即取消
curl -X DELETE http://localhost:8080/task/$TASK_ID
```

**预期结果:**
- 返回取消成功响应
- SSE 流返回 cancelled 状态的 completed 事件

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| HTTP 状态码 | 200 |
| status | success |
| message | 任务已取消 |

---

#### TC-API-008: 取消已完成的任务

**测试目的:** 验证取消已完成任务的错误处理

**前置条件:**
- 任务已执行完成

**测试步骤:**
```bash
curl -X DELETE http://localhost:8080/task/{已完成的task_id}
```

**预期结果:**
- 返回无法取消的响应

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| status | task_completed |
| message | 任务已完成，无法取消 |

---

#### TC-API-009: 取消不存在的任务

**测试目的:** 验证错误处理

**测试步骤:**
```bash
curl -X DELETE http://localhost:8080/task/task-99999999-999999999-xxxx
```

**预期结果:**
- 返回任务不存在响应

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| status | task_not_found |
| message | 任务不存在 |

---

### 2.3 GET /task/status/{task_id}

#### TC-API-010: 查询正在执行的任务状态

**测试目的:** 验证状态查询功能

**前置条件:**
- 有正在执行的任务

**测试步骤:**
```bash
curl -s http://localhost:8080/task/status/{running_task_id}
```

**预期结果:**
```json
{
  "status": "success",
  "task_id": "xxx",
  "task_status": "running",
  "started_at": "2026-04-17T10:30:00Z",
  "elapsed_time": "8s"
}
```

---

#### TC-API-011: 查询已完成任务状态

**测试目的:** 验证已完成状态查询

**测试步骤:**
```bash
curl -s http://localhost:8080/task/status/{completed_task_id}
```

**预期结果:**
```json
{
  "status": "success",
  "task_id": "xxx",
  "task_status": "completed",
  "elapsed_time": "45s"
}
```

---

#### TC-API-012: 查询不存在任务状态

**测试目的:** 验证错误处理

**测试步骤:**
```bash
curl -s http://localhost:8080/task/status/task-99999999-999999999-xxxx
```

**预期结果:**
```json
{
  "status": "task_not_found",
  "task_id": "xxx",
  "message": "任务不存在"
}
```

---

### 2.4 GET /task/history

#### TC-API-013: 查询历史任务列表

**测试目的:** 验证历史查询功能

**测试步骤:**
```bash
curl -s http://localhost:8080/task/history
```

**预期结果:**
```json
{
  "status": "success",
  "total": 5,
  "limit": 20,
  "offset": 0,
  "tasks": [...]
}
```

---

#### TC-API-014: 按状态过滤历史任务

**测试目的:** 验证状态过滤

**测试步骤:**
```bash
curl -s "http://localhost:8080/task/history?status=completed"
```

**预期结果:**
- 只返回 completed 状态的任务

---

#### TC-API-015: 分页查询历史任务

**测试目的:** 验证分页功能

**测试步骤:**
```bash
curl -s "http://localhost:8080/task/history?limit=5&offset=0"
curl -s "http://localhost:8080/task/history?limit=5&offset=5"
```

**预期结果:**
- limit 参数生效
- offset 参数生效

---

#### TC-API-016: 按时间范围过滤历史任务

**测试目的:** 验证时间过滤

**测试步骤:**
```bash
curl -s "http://localhost:8080/task/history?start_time=202604170000&end_time=202604172359"
```

**预期结果:**
- 只返回指定时间范围内的任务

---

### 2.5 GET /task/{task_id}

#### TC-API-017: 查询任务详情

**测试目的:** 验证详情查询，包含完整步骤记录

**测试步骤:**
```bash
curl -s http://localhost:8080/task/{task_id}
```

**预期结果:**
```json
{
  "status": "success",
  "task": {
    "id": "xxx",
    "instruction": "...",
    "status": "completed",
    "start_time": "...",
    "end_time": "...",
    "duration": 45,
    "caller": "anonymous",
    "result": "...",
    "steps": [...]
  }
}
```

---

#### TC-API-018: 查询不存在任务详情

**测试目的:** 验证错误处理

**测试步骤:**
```bash
curl -s http://localhost:8080/task/task-99999999-999999999-xxxx
```

**预期结果:**
```json
{
  "status": "task_not_found",
  "task_id": "xxx",
  "message": "任务不存在"
}
```

---

### 2.6 GET /health

#### TC-API-019: 健康检查

**测试目的:** 验证健康检查功能

**测试步骤:**
```bash
curl -s http://localhost:8080/health | jq .
```

**预期结果:**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "...",
  "checks": {
    "llm": {"status": "healthy", "info": {...}},
    "mcp_servers": {"status": "healthy", "info": [...]},
    "skills": {"status": "healthy", "info": {"count": 0}}
  },
  "metrics": {
    "tasks_running": 0
  }
}
```

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| status | healthy |
| checks.llm.status | healthy |
| checks.mcp_servers.status | healthy |
| checks.skills.status | healthy |

---

### 2.7 GET /skills

#### TC-API-020: 查询 Skills 列表

**测试目的:** 验证 Skills 查询

**测试步骤:**
```bash
curl -s http://localhost:8080/skills | jq .
```

**预期结果:**
```json
{
  "skills": [...],
  "total": 0
}
```

---

#### TC-API-021: Skills 加载后查询

**测试目的:** 随后验证 Skills 加载

**前置条件:**
- skills 目录中有有效的 SKILL.md

**测试步骤:**
```bash
mkdir -p ~/.groot-test/skills/test_skill
cat > ~/.groot-test/skills/test_skill/SKILL.md << 'EOF'
---
name: test_skill
description: "测试技能"
---
# 测试技能
这是一个测试技能。
EOF

sleep 3  # 等待热插拔生效
curl -s http://localhost:8080/skills | jq .
```

**预期结果:**
- skills 列表包含新添加的 skill
- total 增加到 1

---

### 2.8 GET /tools

#### TC-API-022: 查询 MCP 工具列表

**测试目的:** 验证工具查询

**测试步骤:**
```bash
curl -s http://localhost:8080/tools | jq .
```

**预期结果:**
```json
{
  "tools": [
    {"name": "file_read", "description": "读取文件内容", "mcp": "file_operations"},
    {"name": "file_write", "description": "写入文件内容", "mcp": "file_operations"},
    ...
  ],
  "total": 11
}
```

---

## 三、SSE 事件测试用例

### 3.1 SSE 事件结构验证

#### TC-SSE-001: intent 事件验证

**测试目的:** 验证 intent 事件结构

**测试步骤:**
执行任务，捕获第一个事件

**预期结果:**
```
event: intent
data: {"timestamp":"2026-04-17T10:30:00Z"}
```

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| event 类型 | intent |
| timestamp | ISO 格式时间戳 |

---

#### TC-SSE-002: progress 事件验证

**测试目的:** 验证 progress 事件结构

**预期结果:**
```
event: progress
data: {"message":"...","step_id":"...","timestamp":"..."}
```

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| event 类型 | progress |
| message | 非空字符串 |
| step_id | 格式正确 |
| timestamp | ISO 格式时间戳 |

---

#### TC-SSE-003: completed 事件验证（成功）

**测试目的:** 验证成功完成的 completed 事件

**预期结果:**
```
event: completed
data: {"status":"success","timestamp":"...","duration":"...","result":"..."}
```

**验证点:**
| 验证项 | 预期值 |
|--------|--------|
| status | success |
| duration | 格式如 5s, 1m30s |
| result | 非空 |

---

#### TC-SSE-004: completed 事件验证（取消）

**测试目的:** 验证取消的 completed 事件

**预期结果:**
```
event: completed
data: {"status":"cancelled","timestamp":"...","duration":"...","message":"用户主动取消"}
```

---

## 四、核心功能测试用例

### 4.1 Agent 执行功能

#### TC-CORE-001: 简单对话执行

**测试目的:** 验证 Agent 能正常调用 LLM

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "你好"}'
```

**预期结果:**
- LLM 返回有效响应
- result 字段不为空

---

#### TC-CORE-002: 工具调用执行

**测试目的:** 验证 Agent 能正确调用 MCP 工具

**前置条件:**
- 配置 file_operations 的 allowed_paths

**测试步骤:**
```bash
# 配置允许路径
cat > ~/.groot-test/mcp/file_operations.json << 'EOF'
{
  "name": "file_operations",
  "type": "builtin",
  "description": "文件读写和目录操作",
  "isActive": true,
  "tools": ["file_read", "file_write", "directory_list"],
  "restrictions": {
    "allowed_paths": ["~/.groot-test"]
  }
}
EOF

sleep 3

curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我列出 ~/.groot-test 目录下的文件"}'
```

**预期结果:**
- Agent 调用 directory_list 工具
- 返回目录列表

---

#### TC-CORE-003: HTTP 工具调用

**测试目的:** 验证 HTTP 工具调用

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我访问 https://httpbin.org/get 并返回结果"}'
```

**预期结果:**
- Agent 调用 http_get 工具
- 返回 HTTP 响应内容

---

### 4.2 Skills 功能

#### TC-SKILL-001: Skills 加载

**测试目的:** 随后验证 Skills 加载

**测试步骤:**
```bash
mkdir -p ~/.groot-test/skills/test_skill
cat > ~/.groot-test/skills/test_skill/SKILL.md << 'EOF'
---
name: test_skill
description: "测试技能"
---
# 测试技能指令
EOF

curl -s http://localhost:8080/skills | jq .
```

**预期结果:**
- skills 列表包含 test_skill

---

#### TC-SKILL-002: Skills 热插拔（添加）

**测试目的:** 随后验证运行时添加 Skills

**前置条件:**
- 服务已启动

**测试步骤:**
1. 记录当前 skills 数量
2. 创建新的 Skill
3. 等待 2 秒（防抖）
4. 查询 skills 数量

**预期结果:**
- skills 数量增加

---

#### TC-SKILL-003: Skills 热插拔（删除）

**测试目的:** 随后验证运行时删除 Skills

**测试步骤:**
```bash
rm -rf ~/.groot-test/skills/test_skill
sleep 3
curl -s http://localhost:8080/skills | jq .
```

**预期结果:**
- skills 列表不再包含 test_skill

---

#### TC-SKILL-004: Skills 热插拔（修改）

**测试目的:** 随后验证运行时修改 Skills

**测试步骤:**
```bash
cat > ~/.groot-test/skills/test_skill/SKILL.md << 'EOF'
---
name: test_skill_modified
description: "修改后的测试技能"
---
# 修改后的测试技能指令
EOF

sleep 3
curl -s http://localhost:8080/skills | jq .
```

**预期结果:**
- skills 列表更新

---

### 4.3 MCP 功能

#### TC-MCP-001: MCP 加载

**测试目的:** 验证 MCP 配置加载

**测试步骤:**
```bash
curl -s http://localhost:8080/tools | jq '.total'
```

**预期结果:**
- tools 数量 > 0（内置工具）

---

#### TC-MCP-002: MCP 热插拔（添加）

**测试目的:** 随后验证运行时添加 MCP 配置

**测试步骤:**
```bash
cat > ~/.groot-test/mcp/test_mcp.json << 'EOF'
{
  "name": "test_mcp",
  "type": "builtin",
  "description": "测试 MCP",
  "isActive": true,
  "tools": ["test_tool"]
}
EOF

sleep 3
curl -s http://localhost:8080/tools | jq .
```

**预期结果:**
- tools 列表包含新的工具

---

#### TC-MCP-003: MCP 热插拔（删除）

**测试目的:** 随后验证运行时删除 MCP 配置

**测试步骤:**
```bash
rm ~/.groot-test/mcp/test_mcp.json
sleep 3
curl -s http://localhost:8080/tools | jq .
```

**预期结果:**
- tools 列表不再包含该 MCP 的工具

---

### 4.4 存储功能

#### TC-STORAGE-001: 任务创建存储

**测试目的:** 验证任务创建后存储

**测试步骤:**
```bash
TASK_ID=$(curl -s -D - -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "测试存储"}' \
  | grep X-Task-ID | cut -d' ' -f2 | tr -d '\r')

sleep 5

curl -s http://localhost:8080/task/$TASK_ID | jq '.task.id'
```

**预期结果:**
- 返回正确的 task_id

---

#### TC-STORAGE-002: 任务状态更新

**测试目的:** 随后验证任务完成后状态更新

**预期结果:**
- status 更新为 completed

---

#### TC-STORAGE-003: 历史查询

**测试目的:** 随后验证历史任务查询

**预期结果:**
- 能查询到已完成的任务

---

### 4.5 认证功能

#### TC-AUTH-001: 认证关闭时访问

**测试目的:** 随后验证认证关闭时可自由访问

**前置条件:**
- security.auth.enabled = false

**测试步骤:**
```bash
curl -s http://localhost:8080/health
```

**预期结果:**
- 正常返回响应

---

#### TC-AUTH-002: 认证开启时无 Key 访问

**测试目的:** 随后验证认证开启时的拦截

**前置条件:**
- security.auth.enabled = true

**测试步骤:**
```bash
curl -s http://localhost:8080/health
```

**预期结果:**
- HTTP 状态码 401

---

#### TC-AUTH-003: 认证开启时带 Key 访问

**测试目的:** 随后验证认证开启时正确 Key 可访问

**测试步骤:**
```bash
curl -s -H "X-API-Key: valid_key" http://localhost:8080/health
```

**预期结果:**
- 正常返回响应

---

### 4.6 限流功能

#### TC-RATE-001: 并发任务限制

**测试目的:** 随后验证最大并发任务限制

**前置条件:**
- max_concurrent_tasks = 10

**测试步骤:**
同时发起超过限制的请求

**预期结果:**
- 超过限制的请求返回 429

---

### 4.7 错误处理

#### TC-ERROR-001: LLM 连接失败

**测试目的:** 随后验证 LLM 连接失败的错误处理

**前置条件:**
- LLM 服务不可用

**预期结果:**
- 返回错误响应
- 任务状态为 failed

---

#### TC-ERROR-002: 工具调用失败

**测试目的:** 随后验证工具调用失败的错误处理

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我读取 /root/sensitive.txt"}'
```

**预期结果:**
- 工具调用因路径限制失败
- Agent 正确处理错误并返回提示

---

## 五、性能测试用例

### 5.1 响应时间测试

#### TC-PERF-001: 健康检查响应时间

**测试目的:** 随后验证健康检查响应速度

**测试步骤:**
```bash
time curl -s http://localhost:8080/health
```

**预期结果:**
- 响应时间 < 100ms

---

#### TC-PERF-002: 任务执行响应时间

**测试目的:** 随后验证任务执行首字节响应时间

**预期结果:**
- intent 事件响应时间 < 500ms

---

### 5.2 并发测试

#### TC-PERF-003: 并发请求测试

**测试目的:** 随后验证并发处理能力

**测试步骤:**
```bash
for i in {1..5}; do
  curl -X POST http://localhost:8080/task/execute \
    -H "Content-Type: application/json" \
    -d '{"instruction": "测试并发 $i"}' &
done
wait
```

**预期结果:**
- 所有请求正常处理

---

## 六、边界测试用例

### 6.1 输入边界测试

#### TC-EDGE-001: 超长指令

**测试目的:** 随后验证超长指令的处理

**测试步骤:**
```bash
LONG_INSTRUCTION=$(python3 -c "print('测试' * 10000)")
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d "{\"instruction\": \"$LONG_INSTRUCTION\"}"
```

**预期结果:**
- 正常处理或返回限制错误

---

#### TC-EDGE-002: 特殊字符指令

**测试目的:** 随后验证特殊字符处理

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "测试特殊字符: \n\t\r\"\'<>{}[]"}'
```

**预期结果:**
- 正常处理

---

### 6.2 资源边界测试

#### TC-EDGE-003: 大附件

**测试目的:** 随后验证大附件处理

**预期结果:**
- 超过限制返回错误

---

## 七、测试执行顺序

建议按以下顺序执行测试：

1. **环境准备** - 配置测试环境
2. **API 基础测试** - TC-API-001 到 TC-API-022
3. **SSE 事件测试** - TC-SSE-001 到 TC-SSE-004
4. **核心功能测试** - TC-CORE-001 到 TC-CORE-003
5. **Skills 测试** - TC-SKILL-001 到 TC-SKILL-004
6. **MCP 测试** - TC-MCP-001 到 TC-MCP-003
7. **存储测试** - TC-STORAGE-001 到 TC-STORAGE-003
8. **认证测试** - TC-AUTH-001 到 TC-AUTH-003
9. **性能测试** - TC-PERF-001 到 TC-PERF-003
10. **边界测试** - TC-EDGE-001 到 TC-EDGE-003

---

## 八、测试报告模板

每次测试完成后，应记录以下信息：

```
测试日期: YYYY-MM-DD
测试环境:
  - LLM Base URL: 
  - LLM Model: 
  - Groot Version: 

测试结果汇总:
  - 通过数量: X
  - 失败数量: Y
  - 跳过数量: Z

失败测试详情:
  - TC-XXX: 失败原因
  - TC-YYY: 失败原因

建议修复:
  - ...
```

---

## 附录：测试用例总数

| 分类 | 数量 |
|------|------|
| API 端点测试 | 22 |
| SSE 事件测试 | 4 |
| 核心功能测试 | 3 |
| Skills 功能测试 | 4 |
| MCP 功能测试 | 3 |
| 存储功能测试 | 3 |
| 认证功能测试 | 3 |
| 限流功能测试 | 1 |
| 错误处理测试 | 2 |
| 性能测试 | 3 |
| 边界测试 | 3 |
| **总计** | **48** |