# 系统测试报告 —— JWT API Key 认证改造 + 测试库全面整修

- **日期**: 2026-09-03
- **分支**: master（含 JWT API Key 认证功能，commit 42c3f79 及后续测试整修）
- **执行方式**: `GROOT_TEST_PORT=18081 pytest`（独立实例，不影响开发环境 8080）
- **测试环境**: macOS / Python 3.9 venv / SQLite 模式 / Mock LLM（OpenAI 兼容，127.0.0.1:8230，支持流式）

## 一、最终结果

| 指标 | 数值 |
|------|------|
| 用例总数 | **355** |
| 通过 | **326** |
| 失败 | **0** |
| 跳过 | 29（原因见 §四） |
| 耗时 | 3 分 45 秒 |
| 测试文件 | 27 个 |

### 逐文件结果（通过 / 跳过）

| 文件 | 通过 | 跳过 | 覆盖内容 |
|------|-----:|-----:|----------|
| test_api_endpoints | 25 | | /chat、/sess、/web/health、/web/skills、/web/tools |
| test_apikeys_api ★ | 17 | | /web/apikeys 全套（创建校验/重名/token 确定性重取/删除即吊销） |
| test_attachments | 16 | | 附件校验（数量/大小/类型/解码）与历史记录 |
| test_authentication | 15 | | JWT 认证 401/403、权限点、删除即吊销 |
| test_cli_args | 6 | | 命令行参数、GROOT_HOME、认证始终开启 |
| test_cli_commands ★ | 9 | | init/secret 生成/0600、user reset、status、push、--help |
| test_cluster | 9 | | 多实例选举、心跳、故障转移（DB 成员表） |
| test_errors | 17 | | 错误码与错误响应格式 |
| test_groot_md | 10 | | GROOT.md 引导注入 |
| test_hot_reload | 4 | | Skill 增删改实时生效 |
| test_id_formats | 13 | | session_id / chat_id / step_id 格式 |
| test_logging | 8 | 2 | 日志文件、JSON 格式（2 个长时用例常规跳过） |
| test_memory | 9 | | 记忆入库、round_count、状态追踪 |
| test_models_api | 10 | | /web/models CRUD、默认模型保护、脱敏 |
| test_multi_agent | 17 | | X-Agent-Name、子 Agent 注册、init 目录 |
| test_multi_agent_real_llm | | 7 | 需真实 LLM（门控跳过） |
| test_multimodal | 6 | 2 | 图片/音频透传（2 个语义识别用例门控跳过） |
| test_path_config | 8 | | 固定目录、日志路径（相对/绝对） |
| test_performance | 14 | | 并发、409 冲突、chats_running 指标 |
| test_real_llm | | 18 | 需真实 LLM（门控跳过） |
| test_runtime_state | 11 | | 运行状态、取消机制 |
| test_schedule_api | 21 | | schedule 全套端点（DB 预置任务） |
| test_security | 6 | | 附件文件名安全、API Key 不落日志、限流 |
| test_sse_events | 18 | | SSE 事件类型与字段 |
| test_sse_flow | 15 | | SSE 事件顺序、tool_call_id 关联 |
| test_supplementary | 26 | | MCP 连接类型、Skill 依赖、Web 端点权限、健康检查 |
| test_web_auth ★ | 16 | | 登录/登出/setup 409/改密码/Cookie 通行 API |

★ = 本次新增/大幅增补的文件。

## 二、本次测试范围（对应的系统更新）

1. **JWT API Key 认证重构**：认证始终开启；API Key 即 JWT（HS256），元数据存数据库，secret + 元数据可确定性还原 token；删除即吊销；7 个权限点；Web 界面管理（/web/apikeys）。
2. **测试库全面整修**（先于本次运行完成）：删除 5 个已废弃测试文件（chat CLI、schedule CLI 等已移除功能）；修复裸 `/health`、`/skills` 等 40+ 处失效路由引用；适配模型/记忆/调度/集群数据入库；新增 42 个用例补缺口。

## 三、执行过程与发现的缺陷（三轮迭代）

### 第 1 轮：219 失败 → 定位 2 个测试基础设施严重缺陷

| # | 缺陷 | 定性 | 处理 |
|---|------|------|------|
| 1 | `test_cluster.py` 清理逻辑 `pkill -f 'bin/groot'` 杀掉机器上**所有** groot 进程（共享测试服务器、开发环境服务全部被误杀，导致其后 200+ 用例连锁 ConnectionError） | 测试缺陷（历史遗留） | 改为只杀成员表登记 PID + 本模块启动的进程注册表 |
| 2 | cluster 用例硬编码 8080/8081/8082 端口，与开发服务冲突 | 测试缺陷（历史遗留） | 改为专用端口 18201-18203（环境变量可覆盖）；conftest 同款全局 pkill 一并改为按端口精确匹配 |

### 第 2 轮：21 失败 → 逐条定性（源码为准）

| 类别 | 数量 | 明细 |
|------|-----:|------|
| 测试断言过时 | 7 | chat_id 实为 17 位纯数字无 `chat_` 前缀（memory/idgen.go）；detail/session API 消息不含 `attachments` 字段（附件内容不回显） |
| 测试逻辑缺陷 | 3 | GROOT_HOME 用例未先 init（配置不自动生成）；push 在 SQLite 下为本地空跑退出码 0（repofactory: SQLite 用 resourcelocal）而非 ErrSyncDisabled；并发用例未消费首个 SSE 流导致会话持续占用、并发全 409 |
| 环境限制 | 11 | real_llm 6 个语义用例 + 多模态 2 个识别用例（Mock 无法产生智能输出）、multi_agent_real_llm 3 个（远程 LLM 401 无凭据）→ 统一加 `GROOT_TEST_REAL_LLM=1` 门控，默认跳过 |

### 第 3 轮：2 失败（修复中的笔误与状态枚举）→ 修正后全绿

- chat_id 用例残留一行未定义变量引用；
- 消息状态枚举实为 `completed/failed/cancelled`（memory/types.go），断言由 `success/error` 改正。

另修复：test_apikeys_api / test_models_api / test_web_auth 直接读 `GROOT_WEB_USER/GROOT_WEB_PASS` 环境变量导致 24 个用例被静默跳过——统一回落到 conftest 默认凭据，全部转为真实执行并通过。

## 四、跳过用例说明（29 个，均为有意设计）

| 组 | 数量 | 原因 | 启用方式 |
|----|-----:|------|----------|
| test_real_llm | 18 | 语义断言（代码生成/翻译/数学）依赖真实 LLM 智能输出 | 模型库默认模型指向真实 LLM 后 `GROOT_TEST_REAL_LLM=1` |
| test_multi_agent_real_llm | 7 | 编排（call_agent）依赖真实 LLM，且默认端点（dashscope）需有效凭据 | 同上 + 配置 `LLM_BASE_URL/LLM_API_KEY` |
| test_multimodal | 2 | 图片颜色识别 / 文件内容理解需真实多模态 LLM | `GROOT_TEST_REAL_LLM=1` |
| test_logging | 2 | 需长时间运行 / 大量日志的压测型用例 | 手动执行 |

## 五、观察项（非阻塞，供后续参考）

1. **push/pull 在 SQLite 模式的语义**：repofactory 注释称 "SQLite → sync disabled"，且 sync 包备有 `ErrSyncDisabled`（文案"仅在 MySQL/PostgreSQL 模式下可用"），但实际 CLI 路径走 resourcelocal 空跑并输出 "No differences found"、退出码 0。行为无害但与注释/错误文案的意图不一致，建议确认产品预期（明确报错 vs 静默空跑）。
2. **`groot status` 未检测到实例时退出码为 0**：脚本化健康检查无法凭退出码区分服务在/不在。
3. **Key 过期态（expired=true）** 无法在系统测试中预置（token 最短有效期 1 天），由 Go 单测覆盖。
4. MySQL/PostgreSQL 后端与真实 push/pull 全流程未在本轮覆盖（需外部数据库环境）。

## 六、运行方式（复现）

```bash
# 1. 启动 Mock LLM（或真实本地 LLM）于 8230 端口
# 2. 运行（独立端口，避开开发服务）：
cd tests/python
GROOT_TEST_PORT=18081 ./venv/bin/pytest -v
# 启用真实 LLM 用例：
GROOT_TEST_REAL_LLM=1 GROOT_TEST_PORT=18081 ./venv/bin/pytest -v
```

Web 凭据默认 `admin / test-password-2026`（conftest 自动 setup），可用 `GROOT_WEB_USER/GROOT_WEB_PASS` 覆盖。
