# Groot 测试用例汇总

> 本文档记录项目所有测试用例点，便于整体测试和回归验证。

---

## 一、单元测试（Go）

位于 `internal/` 各包目录下的 `*_test.go` 文件。

### 1.1 Chat TUI 测试

| 测试函数 | 测试文件 | 测试内容 |
|---------|---------|---------|
| TestParseCommand | commands_test.go | 命令解析：/exit、/model arg、/session switch id、/skills list、普通文本、空字符串 |
| TestExecuteCommandRouting | commands_test.go | 命令路由：13 条命令 → 对应 Action（quit/clear/render/fetch/export 等） |
| TestMaskAPIKey | commands_test.go | API Key 脱敏：空值、环境变量引用、短 key、长 key |
| TestClassifyEvent | client_test.go | SSE 事件分类：thinking/tool_calls/tool_result/message/finish_reason/error/优先级 |
| TestNewClientDefaults | client_test.go | HTTP 客户端默认值：baseURL 去尾斜杠、modelName 正确设置 |
| TestStatusBarView | model_test.go | 状态栏渲染：非空输出 |
| TestCompletionFilter | model_test.go | 补全过滤：前缀匹配 /mod → /model |
| TestCompletionHide | model_test.go | 补全隐藏：Hide() 后 IsVisible() = false |
| TestCompletionSelectWrap | model_test.go | 补全选择循环：SelectNext/SelectPrev 首尾环绕 |
| TestCompletionFilterNoMatch | model_test.go | 补全无匹配：无匹配项时自动隐藏 |
| TestVisibleWidth | model_test.go | 可见宽度计算：ASCII 和 CJK 字符 |

### 1.2 集群管理测试

位于 `internal/cluster/` 目录。

**选举逻辑** (`election_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestDetermineRole_NoAliveMembers | 无存活成员时成为 leader |
| TestDetermineRole_SelfIsSmallest | 注册编号最小 → leader |
| TestDetermineRole_SelfIsNotSmallest | 注册编号非最小 → follower |
| TestDetermineRole_StaleMembersExcluded | 超时成员被排除后，自身最小 → leader |
| TestDetermineRole_AllStale | 全部超时 → leader |
| TestDetermineRole_SelfStaleOthersAlive | 自身超时但其他存活 → 存活最小者选为 leader |

**文件操作** (`member_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestWriteRegistrationFile | 写入注册文件，验证格式 `{role}\|{host}:{port}\|{pid}` |
| TestListMembers | 列出目录中所有成员，按文件名排序 |
| TestListMembers_EmptyDir | 空目录返回零成员 |
| TestRemoveStaleFile | 删除注册文件，不存在时无操作 |
| TestEnsureMembersDir | 确保 `cluster/members/` 目录存在 |
| TestGenerateRegID | 注册编号格式：17 位数字 `YYYYMMDDHHMMSSmmm` |
| TestFileMtimeUpdates | 覆盖写入后 mtime 更新 |

**Cluster 集成** (`cluster_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestCluster_JoinAsLeader_NoExistingMembers | 无现有成员时加入成为 leader |
| TestCluster_JoinAsFollower_ExistingLeader | 已有 leader 时加入成为 follower |
| TestCluster_Heartbeat_FileLost | 注册文件丢失后重新注册（新 ID） |
| TestCluster_Heartbeat_LeaderCleanupStale | Leader 心跳清理超时文件 |
| TestCluster_Leave | 优雅退出删除注册文件 |
| TestCluster_FollowerPromotionOnLeaderLeave | Leader 退出后 follower 提升 |
| TestCluster_Callbacks_OnBecomeLeader | 成为 leader 时触发回调 |
| TestCluster_Callbacks_OnLoseLeader | 文件丢失时触发失去 leader 回调 |
| TestCluster_Callbacks_OnPromotionFromFollower | Follower 提升为 leader 时触发回调 |
| TestCluster_MultipleInstances_SingleLeader | 3 实例共享 homeDir，恰好 1 个 leader |
| TestCluster_FollowerHeartbeat_NoStaleCleanup | Follower 不清理过期文件（仅 leader 清理） |

---

### 1.3 Web 界面测试

**配置解析** (`internal/config/webconfig_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestWebConfigDefaults | `security.web` 默认值：enabled=false、username=admin、session_ttl=24h |
| TestWebConfigParse | YAML 解析 `security.web` 全部字段 |
| TestWebConfigTemplate | 配置模板含 `security.web` 段与注释说明 |

**登录会话存储** (`internal/api/websession/store_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestStore_CreateAndValidate | 创建令牌后校验通过 |
| TestStore_Expired | 超过 TTL 的令牌校验失败 |
| TestStore_Delete | 删除令牌后校验失败 |
| TestStore_InvalidToken | 未知令牌校验失败 |
| TestStore_LoginLock | 同 IP 连续 5 次失败后锁定 |
| TestStore_LockWindowSlide | 失败记录超出 10 分钟窗口后不再计入 |
| TestStore_ResetOnSuccess | 登录成功清空该 IP 失败计数 |

**登录端点** (`internal/api/handler/webauth_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestWebAuth_LoginSuccess | 密码正确返回 200 并下发 HttpOnly Cookie |
| TestWebAuth_LoginWrongPassword | 密码错误返回 401 |
| TestWebAuth_LoginEmptyConfiguredPassword | 配置密码为空时拒绝登录 |
| TestWebAuth_LoginRateLimited | 连续失败达阈值返回 429 |
| TestWebAuth_Me | `/web/me` 三态：未开启 / 已登录 / 未登录 |
| TestWebAuth_Logout | 登出后令牌失效 |
| TestWebAuth_NilStore | Web 认证关闭（store 为 nil）时三个端点均返回 200 且不 panic |

**认证中间件双凭证** (`internal/api/middleware/auth_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestAuth_CookiePass | 有效 Cookie 通过认证 |
| TestAuth_InvalidCookieFallback | 无效 Cookie 回退到 API Key 校验 |
| TestAuth_APIKeyStillWorks | 原有 X-API-Key 认证不回归 |
| TestAuth_Anonymous | 认证关闭时匿名访问通过 |

**静态托管** (`internal/api/webui_test.go`)

| 测试函数 | 测试内容 |
|---------|---------|
| TestWebUI_ServeIndex | `/ui/` 返回 index.html |
| TestWebUI_ServeAsset | 资源文件返回正确 Content-Type |
| TestWebUI_HistoryFallback | 前端路由路径回退到 index（SPA history 模式） |
| TestWebUI_NotBuilt | 前端未构建时返回提示页 |
| TestWebUI_NoPathTraversal | 路径穿越载荷（`..`、`%2f` 编码、前缀混淆）均不泄漏文件 |

---

## 二、系统测试（Python）

位于 `tests/python/` 目录，使用 pytest 框架。

### 2.1 API 端点测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestChatAPI | test_api_endpoints.py | 聊天 API 基础功能 |
| TestDeleteChatAPI | test_api_endpoints.py | 删除聊天 API |
| TestChatStatusAPI | test_api_endpoints.py | 聊天状态 API |
| TestChatDetailAPI | test_api_endpoints.py | 聊天详情 API |
| TestSessionDetailAPI | test_api_endpoints.py | 会话详情 API |
| TestSessionHistoryAPI | test_api_endpoints.py | 会话历史 API |
| TestHealthAPI | test_api_endpoints.py | 健康检查 API |
| TestSkillsAPI | test_api_endpoints.py | 技能列表 API |
| TestToolsAPI | test_api_endpoints.py | 工具列表 API |
| TestAPIResponseFormat | test_api_endpoints.py | API 响应格式验证 |

### 2.2 附件测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestAttachmentBasic | test_attachments.py | 附件基础功能 |
| TestAttachmentLimits | test_attachments.py | 附件限制（大小、数量） |
| TestAttachmentErrors | test_attachments.py | 附件错误处理 |
| TestAttachmentFilenameSafety | test_attachments.py | 文件名安全性 |
| TestAttachmentStorage | test_attachments.py | 附件存储 |

### 2.3 认证测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestAuthenticationBasic | test_authentication.py | 认证基础功能 |
| TestAuthenticationAllAPIs | test_authentication.py | 所有 API 认证 |
| TestHealthNoAuth | test_authentication.py | 健康检查免认证 |
| TestPermissionSystem | test_authentication.py | 权限系统 |

### 2.4 CLI 参数测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestCommandLineArgs | test_cli_args.py | 命令行参数 |
| TestEnvironmentVariables | test_cli_args.py | 环境变量配置 |
| TestConfigPriority | test_cli_args.py | 配置优先级 |

### 2.5 错误处理测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestErrorResponseFormat | test_errors.py | 错误响应格式 |
| TestErrorCodeList | test_errors.py | 错误码列表 |
| TestSSEErrorHandling | test_errors.py | SSE 错误处理 |
| TestErrorRecovery | test_errors.py | 错误恢复 |
| TestErrorFields | test_errors.py | 错误字段验证 |

### 2.6 热加载测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestGrootMdHotReload | test_hot_reload.py | GROOT.md 热加载 |
| TestGrootMdPosition | test_hot_reload.py | GROOT.md 位置 |
| TestGrootMdMultipleChanges | test_hot_reload.py | GROOT.md 多次变更 |
| TestGrootMdSpecialCases | test_hot_reload.py | GROOT.md 特殊情况 |
| TestSkillsHotReload | test_hot_reload.py | 技能热加载 |
| TestSkillFormat | test_hot_reload.py | 技能格式验证 |

### 2.7 ID 格式测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestSessionIdFormat | test_id_formats.py | Session ID 格式 |
| TestChatIdFormat | test_id_formats.py | Chat ID 格式 |
| TestStepIdFormat | test_id_formats.py | Step ID 格式 |
| TestNestingLevel | test_id_formats.py | 嵌套层级 |
| TestIDGenerationUniqueness | test_id_formats.py | ID 生成唯一性 |

### 2.8 日志测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestLogFormat | test_logging.py | 日志格式 |
| TestLogLevels | test_logging.py | 日志级别 |
| TestLogEvents | test_logging.py | 日志事件 |
| TestLogRetention | test_logging.py | 日志保留 |
| TestLogOutput | test_logging.py | 日志输出 |

### 2.9 记忆系统测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestHistoryJSONFormat | test_memory.py | 历史 JSON 格式 |
| TestChatRecordFormat | test_memory.py | 聊天记录格式 |
| TestMemoryDirectoryStructure | test_memory.py | 记忆目录结构 |
| TestMemoryRoundTracking | test_memory.py | 轮次追踪 |
| TestMemoryStatusTracking | test_memory.py | 状态追踪 |

### 2.10 路径配置测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestDefaultPathConfig | test_path_config.py | 默认路径配置 |
| TestAbsolutePathConfig | test_path_config.py | 绝对路径配置 |
| TestPathResolution | test_path_config.py | 路径解析 |
| TestConfigDirectoryFields | test_path_config.py | 配置目录字段 |
| TestDirectoryAutoCreation | test_path_config.py | 目录自动创建 |
| TestPathConfigIntegration | test_path_config.py | 路径配置集成 |

### 2.11 性能测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestTimeout | test_performance.py | 超时性能 |
| TestLLMPerformance | test_performance.py | LLM 性能 |
| TestMCPPerformance | test_performance.py | MCP 性能 |
| TestReActLimits | test_performance.py | ReAct 限制 |
| TestConcurrency | test_performance.py | 并发性能 |
| TestResourceUsage | test_performance.py | 资源使用 |

### 2.12 真实 LLM 测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestRealLLMBasic | test_real_llm.py | 真实 LLM 基础 |
| TestRealLLMMultiRound | test_real_llm.py | 多轮对话 |
| TestRealLLMToolCall | test_real_llm.py | 工具调用 |
| TestRealLLMComplexTasks | test_real_llm.py | 复杂任务 |
| TestRealLLMErrorHandling | test_real_llm.py | 错误处理 |
| TestRealLLMPerformance | test_real_llm.py | 性能测试 |
| TestRealLLMHistory | test_real_llm.py | 历史记录 |
| TestRealLLMSSEReliability | test_real_llm.py | SSE 可靠性 |

### 2.13 运行状态测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestRuntimeStateBasic | test_runtime_state.py | 运行状态基础 |
| TestRuntimeStateProgress | test_runtime_state.py | 进度追踪 |
| TestRuntimeStateCancel | test_runtime_state.py | 取消机制 |
| TestRuntimeStateMemoryIntegration | test_runtime_state.py | 记忆集成 |
| TestRuntimeStateRunningCount | test_runtime_state.py | 运行计数 |
| TestRuntimeStateActiveChatFields | test_runtime_state.py | 活动聊天字段 |

### 2.14 安全测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestFileOperationsSecurity | test_security.py | 文件操作安全 |
| TestHTTPRequestSecurity | test_security.py | HTTP 请求安全 |
| TestCodeExecutionSecurity | test_security.py | 代码执行安全 |
| TestAttachmentSecurity | test_security.py | 附件安全 |
| TestAuthenticationSecurity | test_security.py | 认证安全 |
| TestInputValidation | test_security.py | 输入验证 |

### 2.15 SSE 流测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestSSEEventOrder | test_sse_events.py | SSE 事件顺序 |
| TestSSEEventFields | test_sse_events.py | SSE 事件字段 |
| TestSSECancelledEvent | test_sse_events.py | SSE 取消事件 |
| TestSSEMultipleRounds | test_sse_events.py | SSE 多轮 |
| TestSSEFlowIntegrity | test_sse_events.py | SSE 流完整性 |

### 2.16 SSE 流程测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestSSEToolCallStrict | test_sse_flow.py | SSE 工具调用严格验证 |
| TestSSEStreamingOutput | test_sse_flow.py | SSE 流式输出 |
| TestSSEPrintDebug | test_sse_flow.py | SSE 调试输出 |

### 2.17 调度 API 测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestScheduleListAPI | test_schedule_api.py | 列出定时任务（空列表、有数据、状态过滤） |
| TestScheduleGetAPI | test_schedule_api.py | 查询任务详情（存在、不存在） |
| TestScheduleDeleteAPI | test_schedule_api.py | 删除定时任务 |
| TestScheduleDisableAPI | test_schedule_api.py | 禁用定时任务（active → disabled） |
| TestScheduleEnableAPI | test_schedule_api.py | 启用定时任务（disabled → active） |
| TestScheduleArchiveAPI | test_schedule_api.py | 归档定时任务（active/disabled → archive） |
| TestScheduleHistoryAPI | test_schedule_api.py | 查询执行历史（空、有记录） |
| TestScheduleAPIAuth | test_schedule_api.py | 调度 API 认证（401 验证） |
| TestScheduleAPIResponseFormat | test_schedule_api.py | 调度 API 响应格式验证 |
| TestScheduleToolsVisible | test_schedule_api.py | 调度工具在 /tools 中可见 |

### 2.18 调度 CLI 测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestScheduleCLIList | test_schedule_cli.py | CLI 列出任务（空、有数据、表头） |
| TestScheduleCLIInspect | test_schedule_cli.py | CLI 查看任务详情（JSON 格式） |
| TestScheduleCLIHistory | test_schedule_cli.py | CLI 查看执行历史（空、有记录） |
| TestScheduleCLIDelete | test_schedule_cli.py | CLI 删除任务 |
| TestScheduleCLIDisable | test_schedule_cli.py | CLI 禁用任务 |
| TestScheduleCLIEnable | test_schedule_cli.py | CLI 启用任务 |
| TestScheduleCLIArchive | test_schedule_cli.py | CLI 归档任务 |
| TestScheduleCLIHelp | test_schedule_cli.py | CLI 帮助信息（--help、子命令帮助） |
| TestScheduleCLIEdgeCases | test_schedule_cli.py | CLI 边界条件（长名称、特殊字符、调度格式） |

### 2.19 补充测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestLLMErrors | test_supplementary.py | LLM 错误 |
| TestMCPToolErrors | test_supplementary.py | MCP 工具错误 |
| TestSkillErrors | test_supplementary.py | 技能错误 |
| TestMCPConnectionTypes | test_supplementary.py | MCP 连接类型 |
| TestSkillsDependencies | test_supplementary.py | 技能依赖 |
| TestPromptValidation | test_supplementary.py | Prompt 验证 |
| TestHealthDetailedChecks | test_supplementary.py | 健康检查详细 |
| TestGracefulShutdown | test_supplementary.py | 优雅关闭 |
| TestConfigHotUpdateBoundaries | test_supplementary.py | 配置热更新边界 |
| TestLLMMultiModelConfig | test_supplementary.py | LLM 多模型配置 |
| TestPermissionBoundaries | test_supplementary.py | 权限边界 |
| TestCancelMechanismDetails | test_supplementary.py | 取消机制详细 |
| TestReActExecutionDetails | test_supplementary.py | ReAct 执行详细 |
| TestSessionHandlingDetails | test_supplementary.py | 会话处理详细 |
| TestMetricsInHealth | test_supplementary.py | 健康检查指标 |

### 2.20 集群管理系统测试

位于 `tests/python/test_cluster.py`。

| 测试类 | 测试函数 | 测试内容 |
|-------|---------|---------|
| TestSingleInstance | test_single_instance_becomes_leader | 单实例启动自动成为 leader |
| TestSingleInstance | test_registration_file_format | 注册文件格式验证（17位ID + 内容格式） |
| TestDualInstance | test_second_instance_becomes_follower | 第二个实例成为 follower |
| TestDualInstance | test_first_instance_is_leader | 先启动（更小编号）的是 leader |
| TestFailover | test_leader_killed_follower_promotes | 杀 leader → follower 提升为 leader |
| TestFailover | test_leader_graceful_shutdown_follower_promotes | Leader 优雅退出 → follower 提升 |
| TestMultipleInstances | test_three_instances_exactly_one_leader | 3 实例恰好 1 leader + 2 follower |
| TestCrashRecovery | test_restarted_old_leader_becomes_follower | 旧 leader 重启后成为 follower |
| TestHeartbeatFileUpdate | test_leader_file_mtime_updates | Leader 心跳持续更新文件 mtime |

---

### 2.21 多 Agent 系统测试（v3.8 后）

设计 `docs/superpowers/specs/2026-05-24-multi-agent-design.md`、计划 `docs/superpowers/plans/2026-05-28-multi-agent-implementation.md`。Python 系统测试由用户后续落地；以下为人工烟囱测试与覆盖范围清单。

**前置准备**：

```bash
mkdir -p ~/.groot/subagents/echo-agent
cat > ~/.groot/subagents/echo-agent/agent.md <<'EOF'
---
description: 回显测试 Agent，把用户输入原样返回
---

# 回显 Agent

收到任何 task 后，直接返回 task 内容。
EOF

go build -o bin/groot ./cmd/groot
./bin/groot &
sleep 2
```

#### 2.21.1 子 Agent 注册（启动期扫描）

| 场景 | 期望 |
|------|------|
| `subagents/` 目录不存在 | 启动正常，`GET /agents` 仅返回 `groot` |
| `subagents/<name>/agent.md` 缺 description | 启动跳过该目录，日志 ERROR；其它 Agent 正常加载 |
| `subagents/<name>/agent.md` 缺失 | 启动跳过该目录 |
| 子目录名 == `groot`（与主 Agent 同名） | 启动跳过，日志 ERROR |
| `subagents/<name>` 是符号链接到目录 | 正常识别为子 Agent |

#### 2.21.2 Solo 模式（X-Agent-Name header）

```bash
# 已注册 → 用子 Agent 执行
curl -X POST http://localhost:8080/chat \
  -H "X-Agent-Name: echo-agent" \
  -H "Content-Type: application/json" \
  -d '{"instruction":"hello","prompt":""}'

# 未注册 → 400 unknown_agent
curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST http://localhost:8080/chat \
  -H "X-Agent-Name: ghost-agent" \
  -H "Content-Type: application/json" \
  -d '{"instruction":"x"}'
# Expected: 400

# X-Agent-Name=groot → 等价于不传（走主 Agent 编排模式）
```

| 场景 | 期望 |
|------|------|
| `X-Agent-Name` 已注册 | 用子 Agent 的 instruction / MCP / skill 执行 |
| `X-Agent-Name` 未注册 | HTTP 400，body 含 `unknown_agent` |
| `X-Agent-Name: groot` | 等价于不传 header，走主 Agent 编排模式 |
| Solo 模式 ChatRecord.AgentName | 持久化到 memory 的字段含子 Agent 名 |

#### 2.21.3 编排模式（call_agent 工具）

要求 GROOT.md 含调度引导段（`groot init` 已自动写入）。

```bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"instruction":"调用 echo-agent，让它回显「test」","prompt":""}'
```

| 场景 | 期望 |
|------|------|
| 主 Agent 工具列表含 `call_agent` | tools 列表内出现，描述含已注册子 Agent 列表 |
| `call_agent(agent_name, task)` 委托 | 子 Agent 接收 task 并执行 |
| 子 Agent SSE 事件透传 | 事件 JSON 含 `agent_name` 字段（子 Agent 名） |
| 子 Agent ChatRecord chatID 含父前缀 | 形如 `<parent_chat_id>_<HHMMSSmmm>_<r4>_<agent_name>` |
| `call_agent` task 超 `max_task_length` | 工具调用拒绝并返回错误说明 |
| 子 Agent 结果超 `max_result_length` | 截断，开头加警告标记 |
| 并发超 `sub_agent.max_concurrency` | FIFO 排队（`semaphore.Weighted`） |
| Token 累加 | 子 Agent token 累计回到父 chat 的 ChatRecord |

#### 2.21.4 API 行为

| 接口 | 验证内容 |
|-----|---------|
| `GET /agents` | `groot` 首位 + 已注册子 Agent；每条含 `name`/`description`/`skills` |
| `GET /skills` | 主 Agent skills（=不传 header 或 `X-Agent-Name: groot`） |
| `GET /skills` + `X-Agent-Name: db-agent` | 返回 db-agent 的 skills（= `subagents/db-agent/skills/`） |
| `GET /skills` + 未知 Agent | HTTP 400 unknown_agent |
| `GET /tools` | 主 Agent MCP 工具 |
| `GET /tools` + `X-Agent-Name: db-agent` | 子 Agent MCP 工具 |
| `GET /chat/status/:sid` | 活跃 chat 时 `progress.sub_agents` 含当前运行的子 Agent 数组 |

#### 2.21.5 TUI `/agent` 命令

| 操作 | 期望 |
|------|------|
| `/agent`（无参） | 弹列表 popup，含 groot + 子 Agent，当前 Agent 标 ✓ |
| `/agent <name>` | 切换并自动新建会话；状态栏 `Agent: <name>` 更新 |
| `/agent groot` | 切回主 Agent，client 不再发送 `X-Agent-Name` header |
| `/agent <未知>` | popup 列表回退（不静默，让用户重选） |
| `/clear` | 不影响当前 Agent 选择 |

#### 2.21.6 Skills 热插拔（subagents/*/skills/）

| 操作 | 期望 |
|------|------|
| 在 `subagents/<name>/skills/` 下新增 `SKILL.md` | watcher 触发回调，日志记录「子 Agent skills 变更触发重新加载」 |
| 在 `subagents/<name>/agent.md` 修改 | watcher 不响应（仅监听 skills 子目录变更） |
| 在 `subagents/<name>/mcp/` 修改 | watcher 不响应 |

#### 2.21.7 init 行为

| 操作 | 期望 |
|------|------|
| `groot init` 全新目录 | 创建 `subagents/` 子目录 |
| `groot init` 全新目录 | 写入 `GROOT.md`，含「子 Agent 调度」段（`call_agent` / 按需调用 / 逐个调用 / 明确传参 / 附件引用 关键词） |
| `groot init` 已有 `GROOT.md` | 跳过不覆盖用户内容 |

### 2.22 Web 界面测试

| 用例编号 | 测试文件 | 测试内容 |
|---------|---------|---------|
| TC-WEB-001 | test_web_auth.py | `/web/me` 返回 authenticated 与 auth_required 字段 |
| TC-WEB-002 | test_web_auth.py | `/ui/` 返回 HTML 页面（构建后为 SPA，未构建为提示页） |
| TC-WEB-003 | test_web_auth.py | `/ui/` 下前端路由路径回退到 index |
| TC-WEB-004 | test_web_auth.py | 路径穿越请求不泄漏二进制外文件 |
| TC-WEB-005 | test_web_auth.py | 错误密码返回 401（触发限速时 429） |
| TC-WEB-006 | test_web_auth.py | 正确登录下发 Cookie，可访问受保护端点，登出后令牌失效 |
| TC-WEB-007 | test_web_auth.py | 无凭证访问受保护端点返回 401 |

登录类用例需 `security.web.enabled: true`，并设置环境变量 `GROOT_WEB_USER` / `GROOT_WEB_PASS`；未开启时自动跳过。

---

## 三、手工验证（Web 界面）

浏览器打开 `http://localhost:8080/ui/`，逐项确认：

| 验证项 | 预期表现 |
|-------|---------|
| 聊天流式输出 | 回答逐字流式渲染，思考过程折叠可展开 |
| 停止生成 | 点击停止按钮后立即中断，已输出内容保留 |
| 附件上传 | 选择文件后随指令发送，模型能读取内容 |
| 历史会话 | 侧栏列出历史会话，点击可载入完整消息流，支持翻页加载 |
| 新建会话 | 清空当前消息流，URL 回到 `/ui/` |
| 主题切换 | 浅色 / 深色 / 跟随系统三种模式生效并持久化（刷新后保持） |
| 设置弹窗 | 五个分类均可打开：通用（主题）、模型、Skills、MCP 工具（按服务分组）、子 Agents |
| 仪表盘 | 服务状态、运行时长、LLM 连通、版本、会话总数、Skills 数、MCP 工具数、MCP 服务列表均有数据 |
| 登录流程 | 开启 `security.web` 后未登录跳转登录页，登录成功进入聊天页，登出回到登录页 |

---

## 四、运行测试

### Go 单元测试

```bash
# 运行所有 internal 包单元测试
go test ./internal/... -v
```

### Python 系统测试

```bash
# 运行所有系统测试
cd tests/python && pytest -v

# 运行指定测试文件
cd tests/python && pytest test_api_endpoints.py -v
```

---

## 五、统计汇总

| 测试类型 | 测试类/函数数 | 测试文件数 |
|---------|-------------|-----------|
| Go 单元测试 | 60 个函数 | 11 |
| Python 系统测试 | 128 个测试 | 27 |

**总计**: 约 188+ 个测试点覆盖核心功能。

---

## 六、已知问题与修复记录

### 6.1 MCP 初始化阻塞等待 roots/list 问题

**发现日期**: 2026-04-24

**问题描述**:

Groot 作为 MCP Client，在初始化时会阻塞等待服务端发送 `roots/list` 请求。但 `roots/list` 是服务端**可选**发送的请求，部分 MCP 服务端不发送此请求，导致 Groot 阻塞 30 秒后超时，管道断开（broken pipe）。

**现象日志**:

```
{"message":"MCP initialized notification sent"}
{"message":"Waiting for roots/list request..."}
{"message":"No roots/list request received: EOF"}        ← 30秒后超时
{"message":"Failed to discover tools... broken pipe"}    ← 管道已断开
{"message":"Registered MCP","name":"file-system","tools":0} ← 工具数为0
```

**问题原因**:

| 原因 | 说明 |
|-----|------|
| 协议理解错误 | `roots/list` 是服务端可选请求，不是必须发送 |
| 阻塞等待 | 代码使用 `reader.ReadString('\n')` 阻塞等待，导致双方互相等待 |
| 流程错误 | 初始化后应直接发送 `tools/list`，不应等待 |

**错误流程**:

```
Groot (Client)                MCP Server
     │                              │
     │─── initialize ──────────────▶│
     │◀── initialize response ─────│
     │─── initialized notification─▶│
     │                              │
     │   (阻塞等待 roots/list...)    │  ← Groot 卡在这里
     │                              │   Server 也在等待下一个请求
     │   (30秒超时)                  │
     │◀── EOF (进程退出) ───────────│
```

**正确流程**:

```
Groot (Client)                MCP Server
     │                              │
     │─── initialize ──────────────▶│
     │◀── initialize response ─────│
     │─── initialized notification─▶│
     │                              │
     │─── tools/list ──────────────▶│  ← 直接发送，不等待
     │◀── tools/list response ─────│
     │                              │
     │   (工具加载成功)              │
```

**修复方案**:

移除阻塞等待 `roots/list` 的代码，初始化完成后直接标记 `initialized = true`，然后发送 `tools/list`。

**修复文件**: `internal/mcp/executor.go`

**修复代码**:

```go
// 旧代码（错误）
e.log.Info("MCP initialized notification sent")
e.log.Info("Waiting for roots/list request...")
line, err = reader.ReadString('\n')  // 阻塞等待
if err != nil {
    e.log.Info("No roots/list request received: " + err.Error())
    proc.initialized = true
    return nil
}
// ... 处理 roots/list ...

// 新代码（正确）
e.log.Info("MCP initialized notification sent")
// Mark as initialized - don't block waiting for roots/list
// roots/list is an optional request from server
proc.initialized = true
e.log.Info("MCP initialization complete")
return nil
```

**验证方法**:

1. 启动 Groot，观察日志无 "Waiting for roots/list request..."
2. MCP 工具加载成功，tools 数 > 0
3. CherryStudio 等其他 MCP Client 能正常访问同一 MCP 服务端

**相关测试用例**:

| 测试点 | 验证内容 |
|-------|---------|
| MCP 工具发现 | `tools/list` 成功返回工具列表 |
| MCP 工具调用 | `tools/call` 正常执行并返回结果 |
| MCP 进程保持 | 进程在初始化后保持运行，不退出 |

**参考文档**: [MCP Protocol Specification - roots](https://spec.modelcontextprotocol.io/specification/2024-11-05/client/roots/)

---

### 6.2 MCP Discovery Context 导致进程退出问题

**发现日期**: 2026-04-24

**问题描述**:

MCP discovery 成功获取工具列表，但后续工具调用时报 `broken pipe` 错误。原因是 discovery 使用的 context 在完成后被 cancel，导致绑定的 MCP 进程被杀死。

**现象日志**:

```
{"message":"Started MCP process for discovery","name":"cmd-exec"}      ← 进程启动
{"message":"MCP initialization complete"}                               ← 初始化成功
{"message":"Discovery response: {...tools...}"}                         ← 工具发现成功
{"message":"Discovered tools from MCP","name":"cmd-exec","count":1}     ← 工具数正确
{"message":"Registered MCP","name":"cmd-exec","tools":1}                 ← 注册成功
... discovery context cancel ...
{"message":"Agent event error: failed to write to stdin: broken pipe"}  ← 后续调用失败
```

**问题原因**:

```go
// manager.go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()  // ← discovery 结束时 cancel

// executor.go（旧代码）
cmd := exec.CommandContext(ctx, config.Command, config.Args...)
// ← 进程绑定到 ctx
// ctx cancel → 进程被杀死！
```

| 原因 | 说明 |
|-----|------|
| Context 绑定错误 | MCP 进程绑定到 discovery 的临时 context |
| defer cancel | discovery 函数返回时调用 cancel，杀死进程 |
| 进程生命周期错误 | 进程应该持久运行，不应随 discovery context 结束 |

**错误流程**:

```
Discovery Phase:
    │─── 创建 context（30秒超时）───────────▶
    │─── 启动 MCP 进程（绑定 context）──────▶
    │─── initialize ───────────────────────▶
    │─── tools/list ────────────────────────▶
    │◀── tools/list response ───────────────│
    │─── defer cancel() ────────────────────▶  ← context cancel
    │                                        │
    │   MCP 进程被杀死！                      │
    │                                        │

Tool Call Phase:
    │─── tools/call ────────────────────────▶  ← stdin 已关闭
    │◀── broken pipe ───────────────────────│  ← 进程已不存在
```

**正确流程**:

```
Discovery Phase:
    │─── 创建 discovery context ─────────────▶
    │─── 启动 MCP 进程（独立 context）────────▶  ← 不绑定 discovery context
    │─── initialize ─────────────────────────▶
    │─── tools/list ─────────────────────────▶
    │◀── tools/list response ────────────────│
    │─── defer cancel() ─────────────────────▶  ← 只 cancel discovery 操作
    │                                        │
    │   MCP 进程继续运行                       │
    │                                        │

Tool Call Phase:
    │─── tools/call ─────────────────────────▶
    │◀── tools/call response ────────────────│  ← 正常执行
```

**修复方案**:

MCP 进程使用 `context.Background()`，不绑定到 discovery 请求的 context。

**修复文件**: `internal/mcp/executor.go`

**修复代码**:

```go
// 旧代码（错误）
func (e *ToolExecutor) discoverStdio(ctx context.Context, config *MCPConfig) ([]ToolDefinition, error) {
    ...
    cmd := exec.CommandContext(ctx, config.Command, config.Args...)  // ← 绑定到 ctx
    ...
}

// 新代码（正确）
func (e *ToolExecutor) discoverStdio(ctx context.Context, config *MCPConfig) ([]ToolDefinition, error) {
    ...
    // Use context.Background() for the process to avoid being killed when discovery context is cancelled
    // The process should persist for tool execution after discovery
    cmd := exec.CommandContext(context.Background(), config.Command, config.Args...)  // ← 独立 context
    ...
}
```

同样修复 `ExecuteStdio` 函数中创建新进程的代码。

**验证方法**:

1. 启动 Groot，观察 MCP 工具加载成功（tools 数 > 0）
2. 发送对话请求，触发 MCP 工具调用
3. 观察日志，工具调用成功返回结果，无 broken pipe 错误
4. 多次调用 MCP 工具，进程保持运行

**相关测试用例**:

| 测试点 | 验证内容 |
|-------|---------|
| MCP 进程持久性 | discovery 后进程继续运行 |
| MCP 工具调用 | tools/call 正常执行，返回结果 |
| MCP 多次调用 | 同一进程处理多次工具调用 |
| 进程状态检查 | `/health` 检查 MCP 服务状态 |

**根本原因分析**:

`exec.CommandContext` 的行为：当 context 被 cancel 时，会向进程发送信号（SIGKILL）终止进程。这是 Go 标准库的设计，用于在 context 超时或取消时清理资源。

MCP 进程需要跨多个请求持久运行，不应绑定到单个请求的 context。