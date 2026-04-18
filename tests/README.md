# Groot API 测试套件

基于 Python pytest 的完整 API 测试套件，覆盖 Groot Agent 服务的所有功能。

## 新版设计变化说明

根据 2026-04-18 设计文档，以下功能已变化：

### 删除的功能（测试已移除）

| 删除项 | 说明 |
|--------|------|
| BoltDB 存储 | 改用文件系统存储（JSON + attachments） |
| 限流配置 | `performance.rate_limit` 已删除 |
| 并发调用限制 | `performance.llm/mcp` 配置已删除 |
| 存储引擎配置 | `storage.engine` 配置已删除 |

### 新增的功能（新增测试）

| 新增项 | 测试文件 |
|--------|----------|
| RuntimeState 模块 | test_runtime_state.py |
| chats/{chat_id}.json | test_memory.py |
| SaveChatRecord/GetChatRecord | test_memory.py |
| step_id 新格式 | test_id_formats.py |

### 格式变化（测试已更新）

| 变化项 | 旧版 | 新版 |
|--------|------|------|
| session_id | `sess_xxx` | 无 `sess_` 前缀 |
| 目录名 | `sess_xxx/` | `{session_id}/` |
| history.json 字段 | `user_content`, `assistant_content` | `instruction`, `result` |
| 附件字段 | `user_attachments`, `assistant_attachments` | `attachments`, `result_attachments` |
| 新增字段 | - | `chat_id`, `status`, `duration`, `steps_count`, `error` |
| 日志事件 | `task_completed` | `chat_completed` |

## 测试用例统计

| 测试模块 | 用例数 | 文件 |
|----------|--------|------|
| API 端点测试 | 25 | test_api_endpoints.py |
| 认证功能测试 | 14 | test_authentication.py |
| 附件处理测试 | 16 | test_attachments.py |
| SSE 事件测试 | 13 | test_sse_events.py |
| Memory/存储测试 | 12 | test_memory.py |
| Skills/MCP 热插拔测试 | 11 | test_hot_reload.py |
| 内置 MCP 工具测试 | 16 | test_builtin_mcp.py |
| 错误处理测试 | 16 | test_errors.py |
| 日志功能测试 | 10 | test_logging.py |
| 命令行参数测试 | 10 | test_cli_args.py |
| 性能测试 | 14 | test_performance.py |
| 安全限制测试 | 15 | test_security.py |
| ID 格式测试 | 17 | test_id_formats.py |
| RuntimeState 测试 | 11 | test_runtime_state.py |
| 补充测试（全覆盖）| 50+ | test_supplementary.py |
| **总计** | **250+** | |

### 补充测试详细模块

test_supplementary.py 包含以下新增测试模块：

| 模块 | 用例数 | 说明 |
|------|--------|------|
| TestLLMErrors | 4 | LLM 错误处理和重试策略 |
| TestMCPToolErrors | 2 | MCP 工具错误和重试 |
| TestSkillErrors | 1 | Skill 错误处理 |
| TestMCPConnectionTypes | 4 | stdio/sse/streamable_http 类型 |
| TestSkillsDependencies | 1 | Skill dependencies 递归调用 |
| TestHTTPRequestLimits | 2 | http_request 超时和大小限制 |
| TestCodeExecutionLimits | 2 | code_execution 安全限制 |
| TestPromptValidation | 2 | prompt 参数验证 |
| TestHealthDetailedChecks | 6 | Health 详细检查 |
| TestMemoryCleanup | 3 | Memory 清理逻辑 |
| TestGracefulShutdown | 2 | 优雅关闭流程 |
| TestConfigHotUpdateBoundaries | 5 | 配置热更新边界 |
| TestLLMMultiModelConfig | 3 | LLM 多模型配置 |
| TestPermissionBoundaries | 3 | 权限边界测试 |
| TestCancelMechanismDetails | 3 | 取消机制详细流程 |
| TestReActExecutionDetails | 3 | ReAct 执行详细步骤 |
| TestSessionHandlingDetails | 3 | 会话处理详细流程 |
| TestMetricsInHealth | 2 | Health metrics 测试 |

## 环境准备

### 安装依赖

```bash
pip install pytest pytest-asyncio requests pyyaml
```

### 启动测试服务

```bash
# 设置环境变量
export GROOT_HOME=/tmp/groot_test
export GROOT_API_KEY=test-api-key-2026

# 创建测试目录
mkdir -p $GROOT_HOME/skills
mkdir -p $GROOT_HOME/mcp
mkdir -p $GROOT_HOME/memory
mkdir -p $GROOT_HOME/logs

# 启动服务
groot -H $GROOT_HOME -p 8080
```

## 运行测试

### 运行所有测试

```bash
# 设置测试环境变量
export GROOT_TEST_HOST=localhost
export GROOT_TEST_PORT=8080
export GROOT_TEST_API_KEY=test-api-key-2026
export GROOT_TEST_HOME=/tmp/groot_test

# 运行测试
cd tests
pytest -v
```

### 使用测试脚本

```bash
cd tests
chmod +x run_tests.sh
./run_tests.sh
```

### 运行特定测试

```bash
# 只运行 API 端点测试
pytest -v tests/test_api_endpoints.py

# 只运行 RuntimeState 测试（新增）
pytest -v tests/test_runtime_state.py

# 只运行新版 ID 格式测试
pytest -v tests/test_id_formats.py

# 按关键词筛选
pytest -k "runtime_state" -v
pytest -k "chat_record" -v
pytest -k "step_id" -v
```

### 生成覆盖率报告

```bash
pip install pytest-cov
pytest --cov=. --cov-report=html --cov-report=term
```

## 测试环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `GROOT_TEST_HOST` | 测试服务地址 | localhost |
| `GROOT_TEST_PORT` | 测试服务端口 | 8080 |
| `GROOT_TEST_API_KEY` | 测试 API Key | test-api-key-2026 |
| `GROOT_TEST_HOME` | 测试工作目录 | /tmp/groot_test |

## 测试文件说明

### test_runtime_state.py（新增）

测试 RuntimeState 模块功能：
- sync.Map 内存管理
- ActiveChat 状态注册
- 进度更新（UpdateProgress）
- 取消功能（Cancel）
- Complete 后保存到 Memory
- 与 Memory 协作

### test_memory.py（已更新）

测试 Memory 存储功能：
- history.json 结构（使用新版字段名：instruction/result）
- chat 记录文件（chats/{chat_id}.json）
- 目录结构（无 sess_ 前缀，chats/ 子目录）
- 新增字段验证：chat_id, status, duration, steps_count, error

### test_id_formats.py（已更新）

测试 ID 格式：
- session_id 格式（无 sess_ 前缀）
- chat_id 格式
- step_id 新格式（{YYYYMMDD}-{HHMMSSmmm}-{random6}）
- nesting_level 字段

### test_performance.py（已更新）

删除了限流测试，保留：
- 超时测试
- ReAct 执行限制测试
- 并发测试
- RuntimeState 并发控制测试

## 联系方式

如有问题，请查阅设计文档：
- docs/superpowers/specs/2026-04-18-groot-agent-design.md