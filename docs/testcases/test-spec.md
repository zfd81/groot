# Groot Agent 测试用例规范

**版本:** 1.0.0
**日期:** 2026-04-17
**状态:** 完整版

---

## 一、测试环境配置

### 1.1 测试前提条件

| 条件 | 说明 |
|------|------|
| LLM 服务 | 需要可用的 LLM API 服务（OpenAI 兼容协议） |
| Groot 服务 | 已编译并可正常启动 |
| 测试目录 | 创建测试专用工作目录 ~/.groot-test |

### 1.2 测试环境变量

```bash
export GROOT_TEST_HOME=~/.groot-test
export GROOT_TEST_PORT=8080
export GROOT_LLM_BASE_URL="http://127.0.0.1:8230/v1"
export GROOT_LLM_API_KEY="your-api-key"
export GROOT_LLM_MODEL="your-model-name"
export GROOT_API_KEY="test-api-key"
```

---

## 二、API 端点测试用例（22个）

### 2.1 POST /task/execute（7个）

#### TC-API-001: 基本任务执行

**测试目的:** 验证基本的任务执行功能

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "你好，请简短介绍一下你自己"}'
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| X-Task-ID Header | 存在，格式正确 |
| Content-Type | text/event-stream |
| intent 事件 | 包含 timestamp |
| progress 事件 | 至少一个 |
| completed 事件 | status=success，result 不为空 |

---

#### TC-API-002: 带 prompt 的任务执行

**测试目的:** 验证自定义 prompt 参数

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "帮我写一个排序算法", "prompt": "你是一个 Python 专家"}'
```

**预期结果:** 响应内容体现 prompt 设定的角色

---

#### TC-API-003: 空指令请求

**测试目的:** 验证参数校验 - instruction 必填

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": ""}'
```

**预期结果:**
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

**预期结果:** HTTP 状态码 400，返回错误信息

---

#### TC-API-005: 带 Base64 文件附件

**测试目的:** 验证 file 类型附件处理

**测试步骤:**
```bash
CONTENT=$(echo "test content" | base64)
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "分析这个文件", "attachments": [{"type": "file", "name": "test.txt", "content": "'$CONTENT'"}]}'
```

**预期结果:** Agent 能识别附件信息

---

#### TC-API-006: 带 URL 附件

**测试目的:** 验证 url 类型附件处理

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "分析这个URL的内容", "attachments": [{"type": "url", "name": "example.html", "content": "https://example.com"}]}'
```

**预期结果:** Agent 能识别 URL 附件

---

#### TC-API-007: 多附件请求

**测试目的:** 验证多附件处理

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "对比分析", "attachments": [{"type": "file", "name": "a.txt", "content": "..."}, {"type": "file", "name": "b.txt", "content": "..."}]}'
```

**预期结果:** Agent 能识别多个附件

---

### 2.2 DELETE /task/{task_id}（3个）

#### TC-API-008: 取消正在执行的任务

**测试目的:** 验证任务取消功能

**测试步骤:**
```bash
# 启动长任务
curl -X POST http://localhost:8080/task/execute -d '{"instruction": "详细分析人工智能历史"}' &
sleep 2
# 取消任务
curl -X DELETE http://localhost:8080/task/{task_id}
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| HTTP 状态码 | 200 |
| status | success |
| message | 任务已取消 |
| SSE completed | status=cancelled |

---

#### TC-API-009: 取消已完成的任务

**测试目的:** 验证已完成任务无法取消

**测试步骤:**
```bash
curl -X DELETE http://localhost:8080/task/{已完成的task_id}
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| status | task_completed |
| message | 任务已完成，无法取消 |

---

#### TC-API-010: 取消不存在的任务

**测试目的:** 验证错误处理

**测试步骤:**
```bash
curl -X DELETE http://localhost:8080/task/task-99999999-xxxx
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| status | task_not_found |
| message | 任务不存在 |

---

### 2.3 GET /task/status/{task_id}（3个）

#### TC-API-011: 查询正在执行的任务状态

**测试目的:** 验证 running 状态查询

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| status | success |
| task_status | running |
| started_at | ISO 时间格式 |
| elapsed_time | 格式如 8s |

---

#### TC-API-012: 查询已完成任务状态

**测试目的:** 验证 completed 状态查询

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| task_status | completed |
| elapsed_time | 格式如 45s |

---

#### TC-API-013: 查询不存在任务状态

**测试目的:** 验证错误处理

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| status | task_not_found |

---

### 2.4 GET /task/history（4个）

#### TC-API-014: 查询历史任务列表

**测试目的:** 验证历史查询默认值

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| status | success |
| total | 任务总数 |
| limit | 20（默认） |
| offset | 0（默认） |

---

#### TC-API-015: 按状态过滤历史任务

**测试目的:** 验证 status 过滤参数

**测试步骤:**
```bash
curl -s "http://localhost:8080/task/history?status=completed"
curl -s "http://localhost:8080/task/history?status=failed"
curl -s "http://localhost:8080/task/history?status=running"
curl -s "http://localhost:8080/task/history?status=cancelled"
```

**预期结果:** 返回的任务状态与过滤参数一致

---

#### TC-API-016: 分页查询历史任务

**测试目的:** 验证 limit 和 offset 参数

**测试步骤:**
```bash
curl -s "http://localhost:8080/task/history?limit=5&offset=0"
curl -s "http://localhost:8080/task/history?limit=100"  # 最大100
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| limit=5 | 返回最多5条 |
| limit=100 | 最多100条（超出截断） |

---

#### TC-API-017: 按时间范围过滤历史任务

**测试目的:** 验证 start_time 和 end_time 参数

**测试步骤:**
```bash
curl -s "http://localhost:8080/task/history?start_time=202604010000&end_time=202604302359"
```

**预期结果:** 返回指定时间范围内的任务

---

### 2.5 GET /task/{task_id}（2个）

#### TC-API-018: 查询任务详情

**测试目的:** 验证详情包含完整步骤记录

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| task.id | 正确的 task_id |
| task.instruction | 原始指令 |
| task.status | 任务状态 |
| task.steps | 步骤记录数组 |
| task.result | 任务结果 |

---

#### TC-API-019: 查询不存在任务详情

**测试目的:** 验证错误处理

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| status | task_not_found |

---

### 2.6 GET /health（1个）

#### TC-API-020: 健康检查

**测试目的:** 验证健康检查返回完整信息

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| status | healthy |
| version | 1.0.0 |
| checks.llm.status | healthy |
| checks.mcp_servers.status | healthy |
| checks.skills.status | healthy |
| metrics.tasks_running | 运行任务数 |

---

### 2.7 GET /skills（2个）

#### TC-API-021: 查询 Skills 列表

**测试目的:** 验证 Skills 查询

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| skills | 数组 |
| total | Skills 数量 |

---

#### TC-API-022: Skills 加载后查询

**测试目的:** 验证 Skills 加载和热插拔

**前置条件:** skills 目录中有有效的 SKILL.md

---

### 2.8 GET /tools（2个）

#### TC-API-023: 查询 MCP 工具列表

**测试目的:** 验证工具查询

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| tools | 数组，包含工具信息 |
| total | 工具数量（>=11，内置工具） |

---

#### TC-API-024: MCP 加载后查询

**测试目的:** 验证 MCP 热插拔

---

---

## 三、限流功能测试用例（4个）

### 3.1 max_concurrent_tasks

#### TC-RATE-001: 并发任务数限制

**测试目的:** 验证最大并发任务数限制（默认10）

**配置:** max_concurrent_tasks = 10

**测试步骤:**
```bash
# 同时发起超过限制的请求
for i in {1..15}; do
  curl -X POST http://localhost:8080/task/execute \
    -H "Content-Type: application/json" \
    -d '{"instruction": "测试并发 $i"}' &
done
wait
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| 前10个请求 | 正常执行 |
| 第11+请求 | HTTP 429 |
| status | rate_limited |

---

### 3.2 max_requests_per_minute

#### TC-RATE-002: 每分钟请求数限制

**测试目的:** 验证每分钟请求数限制（默认60）

**配置:** max_requests_per_minute = 60

**测试步骤:**
```bash
# 快速发起超过60个请求
for i in {1..70}; do
  curl -s http://localhost:8080/health
done
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| 前60个请求 | 正常响应 |
| 第61+请求 | HTTP 429 |
| status | rate_limited |

---

### 3.3 max_requests_per_hour

#### TC-RATE-003: 每小时请求数限制

**测试目的:** 验证每小时请求数限制（默认1000）

**配置:** max_requests_per_hour = 1000

**测试步骤:** 模拟短时间内发送大量请求

**预期结果:** 达到限制后返回 429

---

### 3.4 限流恢复

#### TC-RATE-004: 限流后自动恢复

**测试目的:** 验证限流窗口结束后恢复

**测试步骤:**
1. 触发限流
2. 等待60秒（分钟窗口）
3. 再次请求

**预期结果:** 等待后可正常请求

---

---

## 四、超时功能测试用例（3个）

### 4.1 task_max_duration

#### TC-TIMEOUT-001: 任务最大执行时长

**测试目的:** 验证任务超时终止

**配置:** task_max_duration = 300（秒）

**测试步骤:**
```bash
# 执行一个超长任务
curl -X POST http://localhost:8080/task/execute \
  -d '{"instruction": "详细分析人工智能发展历史（需要很长时间）"}'
```

**预期结果:** 执行超过300秒后终止，返回 timeout 错误

---

### 4.2 llm_call_timeout

#### TC-TIMEOUT-002: LLM 调用超时

**测试目的:** 验证 LLM 调用超时处理

**配置:** llm_call_timeout = 60（秒）

**预期结果:** LLM 调用超过60秒返回 llm_timeout 错误

---

### 4.3 tool_call_timeout

#### TC-TIMEOUT-003: 工具调用超时

**测试目的:** 验证工具调用超时处理

**配置:** tool_call_timeout = 30（秒）

**预期结果:** 工具调用超过30秒返回 tool_call_error

---

---

## 五、LLM 性能测试用例（3个）

### 5.1 max_concurrent_calls

#### TC-LLM-PERF-001: LLM 并发调用限制

**测试目的:** 验证 LLM 并发调用数限制（默认5）

**配置:** max_concurrent_calls = 5

**预期结果:** 同时最多5个 LLM 调用

---

### 5.2 retry_on_failure

#### TC-LLM-PERF-002: LLM 调用失败重试

**测试目的:** 验证 LLM 调用失败后自动重试（默认3次）

**配置:** retry_on_failure = 3, retry_delay = 2

**测试步骤:** 模拟 LLM 服务短暂不可用

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| 第一次失败 | 等待2秒后重试 |
| 重试次数 | 最多3次 |
| 最终失败 | 返回 llm_connection_error |

---

### 5.3 LLM Rate Limit 重试

#### TC-LLM-PERF-003: LLM API 限流重试

**测试目的:** 验证 LLM API 返回 429 时的重试策略

**预期结果:** 等待5秒后重试，最多3次

---

---

## 六、MCP 性能测试用例（1个）

### 6.1 max_concurrent_calls_per_server

#### TC-MCP-PERF-001: MCP 并发调用限制

**测试目的:** 验证每个 MCP 服务并发调用数限制（默认3）

**配置:** max_concurrent_calls_per_server = 3

**预期结果:** 单个 MCP 同时最多3个工具调用

---

---

## 七、ReAct 执行限制测试用例（5个）

### 7.1 max_iterations

#### TC-REACT-001: 最大循环次数限制

**测试目的:** 验证 ReAct 循环次数限制（默认20）

**配置:** max_iterations = 20

**测试步骤:**
```bash
curl -X POST http://localhost:8080/task/execute \
  -d '{"instruction": "不断调用工具，直到达到循环上限"}'
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| 循环次数 | 不超过20次 |
| completed.status | failed |
| error.code | max_iterations_exceeded |

---

### 7.2 max_tokens

#### TC-REACT-002: Token 消耗限制

**测试目的:** 验证 Token 消耗限制（默认100000）

**配置:** max_tokens = 100000

**预期结果:** Token 消耗超过限制后终止

---

### 7.3 step_timeout

#### TC-REACT-003: 单步执行超时

**测试目的:** 验证单步执行超时（默认60秒）

**配置:** step_timeout = 60

**预期结果:** 单步超过60秒返回 timeout 错误

---

### 7.4 error_retry

#### TC-REACT-004: 单步失败重试

**测试目的:** 验证单步失败后重试（默认2次）

**配置:** error_retry = 2

**预期结果:** 单步失败后重试最多2次

---

### 7.5 nesting_max_depth

#### TC-REACT-005: Skills 嵌套深度限制

**测试目的:** 验证 Skills 嵌套深度限制（默认3）

**配置:** nesting_max_depth = 3

**预期结果:** 嵌套深度超过3时终止

---

---

## 八、附件处理测试用例（5个）

### 8.1 max_size

#### TC-ATTACH-001: 单个附件大小限制

**测试目的:** 验证单个附件大小限制（默认50MB）

**配置:** max_size = 50

**测试步骤:**
```bash
# 创建超过50MB的文件
dd if=/dev/zero of=/tmp/large.txt bs=1M count=51
CONTENT=$(base64 /tmp/large.txt)
curl -X POST http://localhost:8080/task/execute \
  -d '{"instruction": "分析文件", "attachments": [{"type": "file", "name": "large.txt", "content": "'$CONTENT'"}]}'
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| HTTP 状态码 | 400 |
| status | attachment_too_large |
| message | 附件大小超过限制 |

---

### 8.2 max_total_size

#### TC-ATTACH-002: 总附件大小限制

**测试目的:** 验证所有附件总大小限制（默认100MB）

**配置:** max_total_size = 100

**测试步骤:** 发送多个附件，总和超过100MB

**预期结果:** 返回 total_attachment_too_large 错误

---

### 8.3 max_count

#### TC-ATTACH-003: 附件数量限制

**测试目的:** 验证附件数量限制（默认10个）

**配置:** max_count = 10

**测试步骤:** 发送超过10个附件

**预期结果:** 返回 attachment_count_exceeded 错误

---

### 8.4 allowed_types

#### TC-ATTACH-004: 附件类型限制

**测试目的:** 验证允许的附件类型

**配置:** allowed_types = [pdf, doc, docx, txt, json, csv, xml, yaml, png, jpg, zip]

**测试步骤:**
```bash
# 发送不允许的类型（如 .exe）
curl -X POST http://localhost:8080/task/execute \
  -d '{"instruction": "分析", "attachments": [{"type": "file", "name": "test.exe", "content": "..."}]}'
```

**预期结果:** 返回 attachment_type_not_allowed 错误

---

### 8.5 temp_directory

#### TC-ATTACH-005: 临时目录清理

**测试目的:** 验证任务完成后临时文件清理

**测试步骤:**
1. 发送带附件的任务
2. 任务完成后检查 temp 目录

**预期结果:** 临时文件被清理

---

---

## 九、认证功能测试用例（9个）

### 9.1 认证开关

#### TC-AUTH-001: 认证关闭时可自由访问

**配置:** security.auth.enabled = false

**预期结果:** 所有 API 无需认证即可访问

---

#### TC-AUTH-002: 认证开启时无 Key 返回 401

**配置:** security.auth.enabled = true

**测试步骤:**
```bash
curl -s http://localhost:8080/health  # 无 X-API-Key
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| HTTP 状态码 | 401 |
| status | unauthorized |
| message | API Key 无效或缺失 |

---

### 9.2 Header 名称

#### TC-AUTH-003: 自定义 Header 名称

**配置:** header_name = X-Custom-Key

**测试步骤:**
```bash
curl -s -H "X-Custom-Key: valid_key" http://localhost:8080/health
```

**预期结果:** 使用自定义 Header 名称可正常访问

---

### 9.3 权限检查（6个权限）

#### TC-AUTH-004: execute 权限

**测试目的:** 验证 POST /task/execute 需要 execute 权限

**配置:** permissions = [execute]

**预期结果:** 可执行任务，其他操作返回 403

---

#### TC-AUTH-005: cancel 权限

**测试目的:** 验证 DELETE /task/{task_id} 需要 cancel 权限

---

#### TC-AUTH-006: status 权限

**测试目的:** 验证 GET /task/status/{task_id} 需要 status 权限

---

#### TC-AUTH-007: history 权限

**测试目的:** 验证 GET /task/history 需要 history 权限

---

#### TC-AUTH-008: detail 权限

**测试目的:** 验证 GET /task/{task_id} 需要 detail 权限

---

#### TC-AUTH-009: skills/tools 权限

**测试目的:** 验证 GET /skills 和 GET /tools 需要相应权限

---

#### TC-AUTH-010: all 权限

**测试目的:** 验证 permissions = [all] 可访问所有 API

---

#### TC-AUTH-011: 权限不足返回 403

**测试步骤:**
```bash
curl -s -H "X-API-Key: limited_key" http://localhost:8080/task/execute
```

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| HTTP 状态码 | 403 |
| status | forbidden |
| message | 权限不足 |

---

---

## 十、存储功能测试用例（5个）

### 10.1 任务创建存储

#### TC-STORAGE-001: 任务创建存储到 BoltDB

**预期结果:** 任务记录正确写入 groot.db

---

### 10.2 任务状态更新

#### TC-STORAGE-002: 任务完成后状态更新

**预期结果:** status 更新为 completed，记录 end_time 和 duration

---

### 10.3 历史查询

#### TC-STORAGE-003: 历史任务可查询

**预期结果:** 可通过 API 查询历史任务

---

### 10.4 retention_days

#### TC-STORAGE-004: 过期数据自动清理

**配置:** retention_days = 7

**预期结果:** 超过7天的任务记录被自动清理

---

### 10.5 cleanup_interval

#### TC-STORAGE-005: 清理任务定时执行

**配置:** cleanup_interval = 24h

**预期结果:** 每24小时执行一次清理

---

---

## 十一、Skills 热插拔测试用例（4个）

### 11.1 Skills 加载

#### TC-SKILL-001: 启动时加载 Skills

**前置条件:** skills 目录中有有效的 SKILL.md

**预期结果:** /skills API 返回加载的 Skills

---

### 11.2 Skills 热插拔开关

#### TC-SKILL-002: 热插拔开启

**配置:** skills.hot_reload.enabled = true

**预期结果:** 添加/修改/删除 SKILL.md 后自动生效

---

#### TC-SKILL-003: 热插拔关闭

**配置:** skills.hot_reload.enabled = false

**预期结果:** 修改 SKILL.md 后需重启服务才生效

---

### 11.3 debounce_delay

#### TC-SKILL-004: 防抖延迟

**配置:** debounce_delay = 2（秒）

**测试步骤:**
1. 创建新 Skill
2. 立即查询 /skills
3. 等待2秒后再次查询

**预期结果:** 等待2秒后才生效

---

---

## 十二、MCP 热插拔测试用例（4个）

### 12.1 MCP 加载

#### TC-MCP-001: 启动时加载 MCP

**前置条件:** mcp 目录中有有效的 .json 配置

**预期结果:** /tools API 返回加载的工具

---

### 12.2 MCP 热插拔开关

#### TC-MCP-002: 热插拔开启

**配置:** mcp.hot_reload.enabled = true

---

#### TC-MCP-003: 热插拔关闭

**配置:** mcp.hot_reload.enabled = false

---

### 12.3 debounce_delay

#### TC-MCP-004: 防抖延迟

**配置:** debounce_delay = 2（秒）

---

---

## 十三、内置 MCP 工具测试用例（11个）

### 13.1 file_operations（7个）

#### TC-TOOL-001: file_read 读取文件

**前置条件:** 配置 allowed_paths

**预期结果:** 正确读取文件内容

---

#### TC-TOOL-002: file_read 路径限制

**测试步骤:** 读取未配置的路径

**预期结果:** 返回 path not allowed 错误

---

#### TC-TOOL-003: file_write 写入文件

**预期结果:** 正确写入文件

---

#### TC-TOOL-004: file_search 搜索文件

**预期结果:** 返回匹配的文件列表

---

#### TC-TOOL-005: directory_list 列出目录

**预期结果:** 返回目录内容列表

---

#### TC-TOOL-006: directory_create 创建目录

**预期结果:** 成功创建目录

---

#### TC-TOOL-007: file_exists 检查存在

**预期结果:** 返回 true/false

---

#### TC-TOOL-008: file_info 获取信息

**预期结果:** 返回文件大小、修改时间等信息

---

### 13.2 http_request（4个）

#### TC-TOOL-009: http_get 发送 GET 请求

**预期结果:** 返回 HTTP 响应内容

---

#### TC-TOOL-010: http_get 域名限制

**配置:** denied_domains = [localhost, 127.0.0.1, 10.*, 192.168.*]

**测试步骤:** 访问被禁止的域名

**预期结果:** 返回 domain denied 错误

---

#### TC-TOOL-011: http_post 发送 POST 请求

**预期结果:** 正确发送 POST 请求并返回响应

---

#### TC-TOOL-012: http_timeout 超时限制

**配置:** timeout = 30

**预期结果:** 超过30秒返回 timeout 错误

---

---

## 十四、SSE 事件测试用例（5个）

### 14.1 intent 事件

#### TC-SSE-001: intent 事件结构

**预期结果:**
```
event: intent
data: {"timestamp":"2026-04-17T10:30:00Z"}
```

---

### 14.2 step_start 事件

#### TC-SSE-002: step_start 事件结构

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| type | skill/tool/llm |
| name | 步骤名称 |
| step_id | 格式正确 |
| timestamp | ISO 时间格式 |
| nesting_level | 嵌套层级 |

---

### 14.3 progress 事件

#### TC-SSE-003: progress 事件结构

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| message | 进度消息 |
| step_id | 关联的步骤 |
| timestamp | ISO 时间格式 |

---

### 14.4 step_end 事件

#### TC-SSE-004: step_end 事件结构

**预期结果:**
| 验证项 | 预期值 |
|--------|--------|
| step_id | 与 step_start 关联 |
| status | success/failed |
| error | 失败时包含错误信息 |

---

### 14.5 completed 事件

#### TC-SSE-005: completed 事件结构

**预期结果（成功）:**
| 验证项 | 预期值 |
|--------|--------|
| status | success |
| duration | 格式如 45s/1m30s |
| result | 任务结果 |

**预期结果（失败）:**
| 验证项 | 预期值 |
|--------|--------|
| status | failed |
| error | 错误信息 |

**预期结果（取消）:**
| 验证项 | 预期值 |
|--------|--------|
| status | cancelled |
| message | 用户主动取消 |

---

---

## 十五、错误处理测试用例（10个）

### 15.1 invalid_request

#### TC-ERROR-001: 参数错误

**预期结果:** HTTP 400，status = invalid_request

---

### 15.2 rate_limited

#### TC-ERROR-002: 请求被限流

**预期结果:** HTTP 429，status = rate_limited

---

### 15.3 llm_connection_error

#### TC-ERROR-003: LLM 连接失败

**测试步骤:** LLM 服务不可用

**预期结果:** status = llm_connection_error

---

### 15.4 llm_rate_limited

#### TC-ERROR-004: LLM API 返回 429

**预期结果:** 自动重试，最终返回 llm_rate_limited

---

### 15.5 llm_timeout

#### TC-ERROR-005: LLM 调用超时

**预期结果:** status = llm_timeout

---

### 15.6 tool_call_error

#### TC-ERROR-006: 工具调用失败

**预期结果:** status = tool_call_error

---

### 15.7 skill_not_found

#### TC-ERROR-007: Skill 不存在

**预期结果:** status = skill_not_found

---

### 15.8 task_timeout

#### TC-ERROR-008: 任务执行超时

**预期结果:** status = task_timeout

---

### 15.9 task_cancelled

#### TC-ERROR-009: 用户取消任务

**预期结果:** status = task_cancelled

---

### 15.10 config_error

#### TC-ERROR-010: 配置错误

**测试步骤:** LLM 配置无效

**预期结果:** HTTP 500，status = config_error

---

---

## 十六、日志功能测试用例（5个）

### 16.1 日志级别

#### TC-LOG-001: 日志级别设置

**配置:** level = info/debug/error

**预期结果:** 日志输出符合配置的级别

---

### 16.2 日志格式

#### TC-LOG-002: JSON 格式日志

**配置:** format = json

**预期结果:** 日志为 JSON 结构化格式

---

### 16.3 日志输出位置

#### TC-LOG-003: 日志输出到文件

**配置:** output = [file]

**预期结果:** 日志写入 logs/groot-{date}.log

---

### 16.4 分类日志

#### TC-LOG-004: 分类日志开关

**配置:** categories.skill.log_input = true

**预期结果:** Skill 日志包含输入内容

---

### 16.5 日志保留

#### TC-LOG-005: 日志文件保留天数

**配置:** max_age = 7

**预期结果:** 超过7天的日志文件被删除

---

---

## 十七、命令行参数测试用例（4个）

### 17.1 -H/--home

#### TC-CLI-001: 指定工作目录

**测试步骤:**
```bash
./groot -H /custom/path
```

**预期结果:** 使用指定的工作目录

---

### 17.2 -p/--port

#### TC-CLI-002: 指定端口

**测试步骤:**
```bash
./groot -p 9090
```

**预期结果:** 服务监听 9090 端口

---

### 17.3 -h/--help

#### TC-CLI-003: 显示帮助

**测试步骤:**
```bash
./groot -h
```

**预期结果:** 显示命令行帮助信息

---

### 17.4 -v/--version

#### TC-CLI-004: 显示版本

**测试步骤:**
```bash
./groot -v
```

**预期结果:** 显示版本号 1.0.0

---

---

## 十八、环境变量测试用例（5个）

### 18.1 OPENAI_API_KEY

#### TC-ENV-001: LLM API Key 环境变量

**配置:** api_key = ${OPENAI_API_KEY}

**预期结果:** 正确读取环境变量

---

### 18.2 GROOT_API_KEY

#### TC-ENV-002: 认证 Key 环境变量

**配置:** key = ${GROOT_API_KEY}

---

### 18.3 GROOT_HOME

#### TC-ENV-003: 工作目录环境变量

**预期结果:** 使用环境变量指定的工作目录

---

### 18.4 环境变量未设置

#### TC-ENV-004: 缺少必要环境变量

**测试步骤:** unset OPENAI_API_KEY

**预期结果:** 服务启动失败或返回 config_error

---

### 18.5 多模型环境变量

#### TC-ENV-005: 多模型 API Key

**配置:** 多个模型配置使用不同环境变量

---

---

## 十九、ID 格式验证测试用例（2个）

### 19.1 task_id 格式

#### TC-ID-001: task_id 格式验证

**预期格式:** task-{YYYYMMDD}-{HHMMSSmmm}-{random4}

**示例:** task-20260417-103000523-a1b2

---

### 19.2 step_id 格式

#### TC-ID-002: step_id 格式验证

**预期格式:** {YYYYMMDD}-{HHMMSSmmm}-{random6}

**示例:** 20260417-103000523-a1b2c3

---

---

## 二十、nesting_level 验证测试用例（1个）

### 20.1 嵌套层级

#### TC-NESTING-001: nesting_level 字段验证

**预期结果:**
| 层级 | 说明 |
|------|------|
| 0 | 主 Skill/主步骤 |
| 1 | 子 Skill/子步骤 |
| 2+ | 更深层嵌套 |

---

---

## 二十一、安全限制测试用例（3个）

### 21.1 allowed_paths

#### TC-SEC-001: 文件路径白名单

**配置:** allowed_paths = [~/.groot-test, /tmp]

**预期结果:** 只能访问白名单路径

---

### 21.2 denied_domains

#### TC-SEC-002: HTTP 域名黑名单

**配置:** denied_domains = [localhost, 127.0.0.1]

**预期结果:** 禁止访问黑名单域名

---

### 21.3 denied_operations

#### TC-SEC-003: 操作黑名单

**配置:** denied_operations = [file_delete]

**预期结果:** 禁止执行删除操作

---

---

## 附录：测试用例统计

| 分类 | 用例数 |
|------|--------|
| API 端点测试 | 24 |
| 限流功能测试 | 4 |
| 超时功能测试 | 3 |
| LLM 性能测试 | 3 |
| MCP 性能测试 | 1 |
| ReAct 执行限制测试 | 5 |
| 附件处理测试 | 5 |
| 认证功能测试 | 11 |
| 存储功能测试 | 5 |
| Skills 热插拔测试 | 4 |
| MCP 热插拔测试 | 4 |
| 内置 MCP 工具测试 | 12 |
| SSE 事件测试 | 5 |
| 错误处理测试 | 10 |
| 日志功能测试 | 5 |
| 命令行参数测试 | 4 |
| 环境变量测试 | 5 |
| ID 格式验证测试 | 2 |
| nesting_level 测试 | 1 |
| 安全限制测试 | 3 |
| **总计** | **98** |