"""
测试用例变更说明
新旧设计文档对比后的测试调整

## 删除的测试（旧版功能已废弃）

1. **限流功能测试** - 删除
   - 旧版有 performance.rate_limit 配置
   - 新版已删除

2. **存储引擎测试** - 删除
   - 旧版有 BoltDB 存储
   - 新版改为文件系统存储

3. **并发调用限制测试** - 删除
   - 旧版有 performance.llm/mcp 并发调用限制
   - 新版已删除

## 新增的测试（新版新增功能）

1. **RuntimeState 测试** - 新增
   - sync.Map 内存管理
   - ActiveChat 状态追踪
   - 进度更新
   - 与 Memory 协作

2. **Chat 记录文件测试** - 新增
   - chats/{chat_id}.json 结构验证
   - SaveChatRecord/GetChatRecord 功能

3. **新版字段验证** - 新增
   - history.json 新字段：chat_id, status, duration, steps_count, error
   - 字段名变化：instruction/result（替代 user_content/assistant_content）

## 修改的测试（格式变化）

1. **session_id 格式测试** - 修改
   - 旧版：sess_xxx
   - 新版：无 sess_ 前缀

2. **目录结构测试** - 修改
   - 新增 chats/ 子目录验证
   - 目录名无 sess_ 前缀

3. **step_id 格式测试** - 新增
   - 新版定义了明确格式

4. **日志事件名称测试** - 修改
   - task_completed → chat_completed
"""

# 以下为需要调整的测试模块列表

DELETED_TESTS = [
    "test_rate_limiting",         # 删除限流测试
    "test_storage_engine",        # 删除存储引擎测试
    "test_concurrent_llm_calls",  # 删除 LLM 并发调用限制测试
    "test_concurrent_mcp_calls",  # 删除 MCP 并发调用限制测试
]

NEW_TESTS = [
    "test_runtime_state",         # 新增 RuntimeState 测试
    "test_chat_record_file",      # 新增 chat 记录文件测试
    "test_new_history_fields",    # 新增新版字段验证
    "test_step_id_format",        # 新增 step_id 格式测试
]

MODIFIED_TESTS = [
    "test_session_id_format",     # 修改 session_id 格式（无前缀）
    "test_memory_directory",      # 修改目录结构（chats/ 子目录）
    "test_history_json_fields",   # 修改字段名
    "test_log_events",            # 修改日志事件名
]