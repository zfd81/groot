# Groot 测试用例汇总

> 本文档记录项目所有测试用例点，便于整体测试和回归验证。

---

## 一、单元测试（Go）

位于 `internal/` 各包目录下的 `*_test.go` 文件。

### 1.1 apitool 包测试

**文件**: `internal/apitool/`

| 测试文件 | 测试函数 | 测试点数 | 测试内容 |
|---------|---------|---------|---------|
| config_test.go | TestAPIToolConfig_GetTimeout | 4 | 超时配置（默认值、负数、自定义、最小值） |
| config_test.go | TestAuthTypeConstants | 4 | 认证类型常量（none、bearer、basic、apikey） |
| config_test.go | TestParameterDefaults | 1 | 参数默认值验证 |
| adapter_test.go | TestNewAPIToolAdapter | 1 | 适配器创建 |
| adapter_test.go | TestAPIToolAdapter_convertType | 11 | 类型转换（string、int、integer、float、number、bool、boolean、array、object、未知、空） |
| adapter_test.go | TestAPIToolAdapter_convertParameters | 1 | 参数转换 |
| adapter_test.go | TestAPIToolAdapter_convertParametersEmpty | 1 | 空参数转换 |
| executor_test.go | TestExecutor_validateParameters | 5 | 参数验证（已提供、缺失、有默认值、非必填、无定义） |
| executor_test.go | TestExecutor_mergeParameters | 3 | 参数合并（合并默认值、覆盖默认值、空参数） |
| executor_test.go | TestExecutor_replaceVariables | 3 | 变量替换（替换参数、未找到保留、空参数） |
| executor_test.go | TestExecutor_replaceVariablesInMap | 1 | Map 变量替换 |
| executor_test.go | TestExecutor_replaceVariablesInBody | 2 | Body 变量替换（嵌套替换、nil 处理） |
| executor_test.go | TestExecutor_replaceInArrayRecursive | 1 | 数组递归替换 |
| executor_test.go | TestExecutor_buildQueryString | 1 | Query 字符串构建 |
| executor_test.go | TestExecutor_buildBody | 3 | Body 构建（JSON、Form、默认） |
| manager_test.go | TestNewManager | 1 | 管理器创建 |
| manager_test.go | TestManager_Register | 1 | 工具注册 |
| manager_test.go | TestManager_RegisterMultiple | 1 | 多工具注册 |
| manager_test.go | TestManager_Get | 2 | 工具获取（已注册、未注册） |
| manager_test.go | TestManager_List | 1 | 工具列表 |
| manager_test.go | TestManager_ListToolNames | 1 | 工具名称列表 |
| manager_test.go | TestManager_Count | 1 | 工具计数 |
| manager_test.go | TestManager_GetExecutor | 1 | 获取执行器 |
| manager_test.go | TestManager_SameNameOverride | 1 | 同名覆盖 |
| validator_test.go | TestExtractEnvVars | 7 | 环境变量提取（URL、Headers、Query、Body、Auth、无变量、去重） |
| validator_test.go | TestExtractEnvVarsFromBodyWithArray | 1 | 数组 Body 环境变量提取 |
| validator_test.go | TestValidateEnvVars | 2 | 环境变量验证（已设置、未设置） |
| validator_test.go | TestCheckToolNameConflict | 3 | 工具名冲突检查（无冲突、有冲突、空列表） |
| validator_test.go | TestValidateAllEnvVars | 2 | 批量环境变量验证（全部已设置、部分未设置） |
| validator_test.go | TestUniqueStrings | 3 | 字符串去重（空列表、无重复、有重复） |

**小计**: 30 个测试函数，78 个测试点

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
| TestDebounceDelay | test_hot_reload.py | 防抖延迟 |

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
| TestMemoryCleanup | test_memory.py | 记忆清理 |
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

### 2.17 补充测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestLLMErrors | test_supplementary.py | LLM 错误 |
| TestMCPToolErrors | test_supplementary.py | MCP 工具错误 |
| TestSkillErrors | test_supplementary.py | 技能错误 |
| TestMCPConnectionTypes | test_supplementary.py | MCP 连接类型 |
| TestSkillsDependencies | test_supplementary.py | 技能依赖 |
| TestPromptValidation | test_supplementary.py | Prompt 验证 |
| TestHealthDetailedChecks | test_supplementary.py | 健康检查详细 |
| TestMemoryCleanup | test_supplementary.py | 记忆清理补充 |
| TestGracefulShutdown | test_supplementary.py | 优雅关闭 |
| TestConfigHotUpdateBoundaries | test_supplementary.py | 配置热更新边界 |
| TestLLMMultiModelConfig | test_supplementary.py | LLM 多模型配置 |
| TestPermissionBoundaries | test_supplementary.py | 权限边界 |
| TestCancelMechanismDetails | test_supplementary.py | 取消机制详细 |
| TestReActExecutionDetails | test_supplementary.py | ReAct 执行详细 |
| TestSessionHandlingDetails | test_supplementary.py | 会话处理详细 |
| TestMetricsInHealth | test_supplementary.py | 健康检查指标 |

### 2.18 API 工具测试

| 测试类 | 测试文件 | 测试内容 |
|-------|---------|---------|
| TestAPIToolConfigLoading | test_apitool.py | 配置加载（单个/多个/无效 JSON） |
| TestAPIToolEnvVarValidation | test_apitool.py | 环境变量验证（缺失/设置/URL/Body） |
| TestAPIToolNameConflict | test_apitool.py | 名称冲突（同名覆盖） |
| TestAPIToolParameters | test_apitool.py | 参数定义（有参数/无参数） |
| TestAPIToolAuthTypes | test_apitool.py | 认证类型（bearer/basic/apikey/无认证） |
| TestAPIToolDirectory | test_apitool.py | 目录处理（空目录/目录不存在） |

---

## 三、运行测试

### Go 单元测试

```bash
# 运行所有 internal 包单元测试
go test ./internal/... -v

# 运行指定包测试
go test ./internal/apitool/... -v
```

### Python 系统测试

```bash
# 运行所有系统测试
cd tests/python && pytest -v

# 运行指定测试文件
cd tests/python && pytest test_api_endpoints.py -v
```

---

## 四、统计汇总

| 测试类型 | 测试类/函数数 | 测试文件数 |
|---------|-------------|-----------|
| Go 单元测试 | 30 个函数 / 78 个测试点 | 5 |
| Python 系统测试 | 99 个类 | 24 |

**总计**: 约 193+ 个测试点覆盖核心功能。