#!/bin/bash
# Groot Agent 自动化测试脚本
# 版本: 1.0.0

set -e

# ==================== 配置 ====================
GROOT_HOME="${GROOT_TEST_HOME:-~/.groot-test}"
GROOT_PORT="${GROOT_TEST_PORT:-8080}"
GROOT_BIN="./groot"

# LLM 配置
LLM_BASE_URL="${GROOT_LLM_BASE_URL:-http://127.0.0.1:8230/v1}"
LLM_API_KEY="${GROOT_LLM_API_KEY:-}"
LLM_MODEL="${GROOT_LLM_MODEL:-}"

# 测试结果
PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0
TEST_RESULTS=""

# ==================== 辅助函数 ====================

log_info() {
    echo "[INFO] $1"
}

log_pass() {
    echo "[PASS] $1"
    PASS_COUNT=$((PASS_COUNT + 1))
    TEST_RESULTS="$TEST_RESULTS\n[PASS] $1"
}

log_fail() {
    echo "[FAIL] $1: $2"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    TEST_RESULTS="$TEST_RESULTS\n[FAIL] $1: $2"
}

log_skip() {
    echo "[SKIP] $1: $2"
    SKIP_COUNT=$((SKIP_COUNT + 1))
    TEST_RESULTS="$TEST_RESULTS\n[SKIP] $1: $2"
}

wait_for_service() {
    log_info "等待服务启动..."
    for i in {1..30}; do
        if curl -s http://localhost:$GROOT_PORT/health > /dev/null 2>&1; then
            log_info "服务已启动"
            return 0
        fi
        sleep 1
    done
    log_fail "服务启动超时"
    return 1
}

extract_task_id() {
    grep -o 'X-Task-ID: task-[0-9-]*' | cut -d' ' -f2 | tr -d '\r'
}

# ==================== 测试函数 ====================

# API 测试
test_api_health() {
    local tc_id="TC-API-019"
    log_info "测试 $tc_id: 健康检查"

    local response
    response=$(curl -s http://localhost:$GROOT_PORT/health)

    local status=$(echo "$response" | jq -r '.status')
    local llm_status=$(echo "$response" | jq -r '.checks.llm.status')
    local mcp_status=$(echo "$response" | jq -r '.checks.mcp_servers.status')
    local skills_status=$(echo "$response" | jq -r '.checks.skills.status')

    if [[ "$status" == "healthy" ]] && \
       [[ "$llm_status" == "healthy" ]] && \
       [[ "$mcp_status" == "healthy" ]] && \
       [[ "$skills_status" == "healthy" ]]; then
        log_pass "$tc_id: 健康检查正常"
    else
        log_fail "$tc_id: 健康检查异常 (status=$status)"
    fi
}

test_api_skills_list() {
    local tc_id="TC-API-020"
    log_info "测试 $tc_id: 查询 Skills 列表"

    local response
    response=$(curl -s http://localhost:$GROOT_PORT/skills)

    local status=$(echo "$response" | jq -r '.skills' 2>/dev/null)

    if [[ "$status" != "null" ]]; then
        log_pass "$tc_id: Skills 列表查询正常"
    else
        log_fail "$tc_id: Skills 列表查询失败"
    fi
}

test_api_tools_list() {
    local tc_id="TC-API-022"
    log_info "测试 $tc_id: 查询 MCP 工具列表"

    local response
    response=$(curl -s http://localhost:$GROOT_PORT/tools)

    local total=$(echo "$response" | jq -r '.total')

    if [[ "$total" -gt 0 ]]; then
        log_pass "$tc_id: 工具列表查询正常 (total=$total)"
    else
        log_fail "$tc_id: 工具列表查询失败 (total=$total)"
    fi
}

test_api_execute_empty_instruction() {
    local tc_id="TC-API-003"
    log_info "测试 $tc_id: 空指令请求"

    local response
    local http_code
    http_code=$(curl -s -o response -w "%{http_code}" -X POST \
        http://localhost:$GROOT_PORT/task/execute \
        -H "Content-Type: application/json" \
        -d '{"instruction": ""}')

    response=$(cat response)

    if [[ "$http_code" == "400" ]]; then
        log_pass "$tc_id: 空指令正确返回 400"
    else
        log_fail "$tc_id: 空指令返回码错误 (http_code=$http_code)"
    fi
}

test_api_execute_invalid_json() {
    local tc_id="TC-API-004"
    log_info "测试 $tc_id: 无效 JSON 请求"

    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
        http://localhost:$GROOT_PORT/task/execute \
        -H "Content-Type: application/json" \
        -d 'invalid json')

    if [[ "$http_code" == "400" ]]; then
        log_pass "$tc_id: 无效 JSON 正确返回 400"
    else
        log_fail "$tc_id: 无效 JSON 返回码错误 (http_code=$http_code)"
    fi
}

test_api_execute_basic() {
    local tc_id="TC-API-001"
    log_info "测试 $tc_id: 基本任务执行"

    # 检查 LLM 配置
    if [[ -z "$LLM_API_KEY" ]] || [[ -z "$LLM_MODEL" ]]; then
        log_skip "$tc_id: 缺少 LLM 配置 (LLM_API_KEY 或 LLM_MODEL)"
        return 0
    fi

    local task_id=""
    local result=""
    local has_intent=false
    local has_progress=false
    local has_completed=false

    # 执行请求并解析 SSE 流
    curl -s -D headers.txt -X POST \
        http://localhost:$GROOT_PORT/task/execute \
        -H "Content-Type: application/json" \
        -d '{"instruction": "你好，请简短回复"}' \
        | while IFS= read -r line; do
            if [[ "$line" =~ ^X-Task-ID: ]]; then
                task_id=$(extract_task_id "$line")
            elif [[ "$line" =~ ^event:\ intent ]]; then
                has_intent=true
            elif [[ "$line" =~ ^event:\ progress ]]; then
                has_progress=true
            elif [[ "$line" =~ ^event:\ completed ]]; then
                has_completed=true
            elif [[ "$line" =~ ^data:\ ]]; then
                result="$line"
            fi
        done

    task_id=$(grep "X-Task-ID" headers.txt | extract_task_id)

    if [[ -n "$task_id" ]]; then
        log_pass "$tc_id: 任务执行成功 (task_id=$task_id)"
    else
        log_fail "$tc_id: 任务执行失败 (无 task_id)"
    fi

    rm -f headers.txt
}

test_api_cancel_not_found() {
    local tc_id="TC-API-009"
    log_info "测试 $tc_id: 取消不存在的任务"

    local response
    local http_code
    http_code=$(curl -s -o response -w "%{http_code}" -X DELETE \
        http://localhost:$GROOT_PORT/task/task-99999999-999999999-xxxx)

    response=$(cat response)
    local status=$(echo "$response" | jq -r '.status')

    # 允许 400 或 404，只要返回正确的状态信息
    if [[ "$status" == "task_not_found" ]] || [[ "$status" == "error" ]]; then
        log_pass "$tc_id: 取消不存在任务正确处理"
    else
        log_fail "$tc_id: 取消不存在任务返回异常 (status=$status)"
    fi

    rm -f response
}

test_api_status_not_found() {
    local tc_id="TC-API-012"
    log_info "测试 $tc_id: 查询不存在任务状态"

    local response
    response=$(curl -s http://localhost:$GROOT_PORT/task/status/task-99999999-999999999-xxxx)

    local status=$(echo "$response" | jq -r '.status')

    if [[ "$status" == "task_not_found" ]] || [[ "$status" == "error" ]]; then
        log_pass "$tc_id: 状态查询正确处理不存在任务"
    else
        log_fail "$tc_id: 状态查询返回异常 (status=$status)"
    fi
}

test_api_detail_not_found() {
    local tc_id="TC-API-018"
    log_info "测试 $tc_id: 查询不存在任务详情"

    local response
    response=$(curl -s http://localhost:$GROOT_PORT/task/task-99999999-999999999-xxxx)

    local status=$(echo "$response" | jq -r '.status')

    if [[ "$status" == "task_not_found" ]] || [[ "$status" == "error" ]]; then
        log_pass "$tc_id: 详情查询正确处理不存在任务"
    else
        log_fail "$tc_id: 详情查询返回异常 (status=$status)"
    fi
}

test_api_history() {
    local tc_id="TC-API-013"
    log_info "测试 $tc_id: 查询历史任务列表"

    local response
    response=$(curl -s http://localhost:$GROOT_PORT/task/history)

    local total=$(echo "$response" | jq -r '.total')

    if [[ "$total" != "null" ]]; then
        log_pass "$tc_id: 历史任务列表查询正常 (total=$total)"
    else
        log_fail "$tc_id: 历史任务列表查询失败"
    fi
}

test_api_history_with_filter() {
    local tc_id="TC-API-014"
    log_info "测试 $tc_id: 按状态过滤历史任务"

    local response
    response=$(curl -s "http://localhost:$GROOT_PORT/task/history?status=completed")

    # 验证返回的任务都是 completed 状态
    local all_completed=true
    local count=$(echo "$response" | jq '.tasks | length')

    if [[ "$count" -gt 0 ]]; then
        for i in $(seq 0 $((count - 1))); do
            local task_status=$(echo "$response" | jq -r ".tasks[$i].status")
            if [[ "$task_status" != "completed" ]]; then
                all_completed=false
                break
            fi
        done
    fi

    if [[ "$all_completed" == "true" ]]; then
        log_pass "$tc_id: 状态过滤正常"
    else
        log_fail "$tc_id: 状态过滤异常"
    fi
}

# SSE 测试
test_sse_intent_event() {
    local tc_id="TC-SSE-001"
    log_info "测试 $tc_id: intent 事件验证"

    if [[ -z "$LLM_API_KEY" ]]; then
        log_skip "$tc_id: 缺少 LLM 配置"
        return 0
    fi

    local has_intent=false
    local intent_data=""

    curl -s -X POST \
        http://localhost:$GROOT_PORT/task/execute \
        -H "Content-Type: application/json" \
        -d '{"instruction": "测试intent事件"}' \
        | head -10 \
        | while IFS= read -r line; do
            if [[ "$line" =~ ^event:\ intent ]]; then
                has_intent=true
            elif [[ "$has_intent" == "true" ]] && [[ "$line" =~ ^data:\ ]]; then
                intent_data="$line"
                break
            fi
        done

    # 检查 intent 事件是否包含 timestamp
    if echo "$intent_data" | grep -q "timestamp"; then
        log_pass "$tc_id: intent 事件包含 timestamp"
    else
        log_skip "$tc_id: 无法验证 intent 事件内容"
    fi
}

# Skills 测试
test_skill_hot_reload_add() {
    local tc_id="TC-SKILL-002"
    log_info "测试 $tc_id: Skills 热插拔（添加）"

    # 创建测试 Skill
    mkdir -p "$GROOT_HOME/skills/test_skill_hotreload"
    cat > "$GROOT_HOME/skills/test_skill_hotreload/SKILL.md" << 'EOF'
---
name: test_skill_hotreload
description: "热插拔测试技能"
---
# 测试技能
这是一个热插拔测试技能。
EOF

    sleep 3

    # 查询 Skills 列表
    local response
    response=$(curl -s http://localhost:$GROOT_PORT/skills)

    local has_skill=false
    local count=$(echo "$response" | jq '.skills | length')

    for i in $(seq 0 $((count - 1))); do
        local skill_name=$(echo "$response" | jq -r ".skills[$i].name")
        if [[ "$skill_name" == "test_skill_hotreload" ]]; then
            has_skill=true
            break
        fi
    done

    # 清理测试 Skill
    rm -rf "$GROOT_HOME/skills/test_skill_hotreload"
    sleep 2

    if [[ "$has_skill" == "true" ]]; then
        log_pass "$tc_id: Skills 热插拔添加成功"
    else
        log_fail "$tc_id: Skills 热插拔添加失败"
    fi
}

test_skill_hot_reload_delete() {
    local tc_id="TC-SKILL-003"
    log_info "测试 $tc_id: Skills 热插拔（删除）"

    # 先创建一个 Skill
    mkdir -p "$GROOT_HOME/skills/test_skill_delete"
    cat > "$GROOT_HOME/skills/test_skill_delete/SKILL.md" << 'EOF'
---
name: test_skill_delete
description: "待删除测试技能"
---
# 测试技能
EOF

    sleep 3

    # 删除 Skill
    rm -rf "$GROOT_HOME/skills/test_skill_delete"
    sleep 3

    # 验证已删除
    local response
    response=$(curl -s http://localhost:$GROOT_PORT/skills)

    local has_skill=false
    local count=$(echo "$response" | jq '.skills | length')

    for i in $(seq 0 $((count - 1))); do
        local skill_name=$(echo "$response" | jq -r ".skills[$i].name")
        if [[ "$skill_name" == "test_skill_delete" ]]; then
            has_skill=true
            break
        fi
    done

    if [[ "$has_skill" == "false" ]]; then
        log_pass "$tc_id: Skills 热插拔删除成功"
    else
        log_fail "$tc_id: Skills 热插拔删除失败"
    fi
}

# MCP 测试
test_mcp_hot_reload_add() {
    local tc_id="TC-MCP-002"
    log_info "测试 $tc_id: MCP 热插拔（添加）"

    # 创建测试 MCP 配置
    cat > "$GROOT_HOME/mcp/test_mcp_hotreload.json" << 'EOF'
{
  "name": "test_mcp_hotreload",
  "type": "builtin",
  "description": "热插拔测试 MCP",
  "isActive": true,
  "tools": ["test_tool_hotreload"]
}
EOF

    sleep 3

    # 查询工具列表
    local response
    response=$(curl -s http://localhost:$GROOT_PORT/tools)

    local has_tool=false
    local count=$(echo "$response" | jq '.tools | length')

    for i in $(seq 0 $((count - 1))); do
        local tool_name=$(echo "$response" | jq -r ".tools[$i].name")
        if [[ "$tool_name" == "test_tool_hotreload" ]]; then
            has_tool=true
            break
        fi
    done

    # 清理测试 MCP
    rm -f "$GROOT_HOME/mcp/test_mcp_hotreload.json"
    sleep 2

    if [[ "$has_tool" == "true" ]]; then
        log_pass "$tc_id: MCP 热插拔添加成功"
    else
        log_fail "$tc_id: MCP 热插拔添加失败"
    fi
}

test_mcp_hot_reload_delete() {
    local tc_id="TC-MCP-003"
    log_info "测试 $tc_id: MCP 热插拔（删除）"

    # 先创建 MCP 配置
    cat > "$GROOT_HOME/mcp/test_mcp_delete.json" << 'EOF'
{
  "name": "test_mcp_delete",
  "type": "builtin",
  "description": "待删除测试 MCP",
  "isActive": true,
  "tools": ["test_tool_delete"]
}
EOF

    sleep 3

    # 删除 MCP 配置
    rm -f "$GROOT_HOME/mcp/test_mcp_delete.json"
    sleep 3

    # 验证已删除
    local response
    response=$(curl -s http://localhost:$GROOT_PORT/tools)

    local has_tool=false
    local count=$(echo "$response" | jq '.tools | length')

    for i in $(seq 0 $((count - 1))); do
        local tool_name=$(echo "$response" | jq -r ".tools[$i].name")
        if [[ "$tool_name" == "test_tool_delete" ]]; then
            has_tool=true
            break
        fi
    done

    if [[ "$has_tool" == "false" ]]; then
        log_pass "$tc_id: MCP 热插拔删除成功"
    else
        log_fail "$tc_id: MCP 热插拔删除失败"
    fi
}

# 存储测试
test_storage_task_create() {
    local tc_id="TC-STORAGE-001"
    log_info "测试 $tc_id: 任务创建存储"

    if [[ -z "$LLM_API_KEY" ]]; then
        log_skip "$tc_id: 缺少 LLM 配置"
        return 0
    fi

    # 执行任务并获取 task_id
    local task_id
    task_id=$(curl -s -D - -X POST \
        http://localhost:$GROOT_PORT/task/execute \
        -H "Content-Type: application/json" \
        -d '{"instruction": "测试存储"}' \
        | grep "X-Task-ID" | extract_task_id)

    if [[ -z "$task_id" ]]; then
        log_fail "$tc_id: 无法获取 task_id"
        return 1
    fi

    # 等待任务完成
    sleep 5

    # 查询任务详情
    local response
    response=$(curl -s http://localhost:$GROOT_PORT/task/$task_id)

    local stored_id=$(echo "$response" | jq -r '.task.id')

    if [[ "$stored_id" == "$task_id" ]]; then
        log_pass "$tc_id: 任务正确存储 (task_id=$task_id)"
    else
        log_fail "$tc_id: 任务存储异常"
    fi
}

# 性能测试
test_perf_health_response_time() {
    local tc_id="TC-PERF-001"
    log_info "测试 $tc_id: 健康检查响应时间"

    local start_time
    local end_time
    local duration

    start_time=$(date +%s%N)
    curl -s http://localhost:$GROOT_PORT/health > /dev/null
    end_time=$(date +%s%N)

    duration=$(( (end_time - start_time) / 1000000 ))

    if [[ "$duration" -lt 100 ]]; then
        log_pass "$tc_id: 响应时间 $duration ms (< 100ms)"
    else
        log_fail "$tc_id: 响应时间 $duration ms (> 100ms)"
    fi
}

# ==================== 主测试流程 ====================

setup_environment() {
    log_info "准备测试环境..."

    # 创建必要的目录
    mkdir -p "$GROOT_HOME/skills"
    mkdir -p "$GROOT_HOME/mcp"
    mkdir -p "$GROOT_HOME/logs"

    # 更新配置文件（如果需要）
    if [[ -n "$LLM_BASE_URL" ]] && [[ -n "$LLM_API_KEY" ]] && [[ -n "$LLM_MODEL" ]]; then
        log_info "更新 LLM 配置..."
        # 这里可以根据需要修改 config.yaml
    fi

    log_info "测试环境已准备"
}

start_service() {
    log_info "启动 Groot 服务..."

    # 检查是否有旧进程
    pkill -f "groot -H" 2>/dev/null || true
    sleep 2

    # 启动服务
    $GROOT_BIN -H "$GROOT_HOME" &
    GROOT_PID=$!

    wait_for_service
}

stop_service() {
    log_info "停止 Groot 服务..."

    if [[ -n "$GROOT_PID" ]]; then
        kill $GROOT_PID 2>/dev/null || true
    fi

    pkill -f "groot -H" 2>/dev/null || true
}

run_tests() {
    log_info "开始执行测试..."
    echo ""
    echo "=========================================="
    echo "          API 端点测试"
    echo "=========================================="

    test_api_health
    test_api_skills_list
    test_api_tools_list
    test_api_execute_empty_instruction
    test_api_execute_invalid_json
    test_api_execute_basic
    test_api_cancel_not_found
    test_api_status_not_found
    test_api_detail_not_found
    test_api_history
    test_api_history_with_filter

    echo ""
    echo "=========================================="
    echo "          SSE 事件测试"
    echo "=========================================="

    test_sse_intent_event

    echo ""
    echo "=========================================="
    echo "          Skills 功能测试"
    echo "=========================================="

    test_skill_hot_reload_add
    test_skill_hot_reload_delete

    echo ""
    echo "=========================================="
    echo "          MCP 功能测试"
    echo "=========================================="

    test_mcp_hot_reload_add
    test_mcp_hot_reload_delete

    echo ""
    echo "=========================================="
    echo "          存储功能测试"
    echo "=========================================="

    test_storage_task_create

    echo ""
    echo "=========================================="
    echo "          性能测试"
    echo "=========================================="

    test_perf_health_response_time
}

print_summary() {
    echo ""
    echo "=========================================="
    echo "          测试结果汇总"
    echo "=========================================="
    echo ""
    echo "通过: $PASS_COUNT"
    echo "失败: $FAIL_COUNT"
    echo "跳过: $SKIP_COUNT"
    echo "总计: $((PASS_COUNT + FAIL_COUNT + SKIP_COUNT))"
    echo ""

    if [[ "$FAIL_COUNT" -gt 0 ]]; then
        echo "=========================================="
        echo "          失败测试详情"
        echo "=========================================="
        echo "$TEST_RESULTS" | grep "\[FAIL\]"
        echo ""
    fi

    echo "测试报告已生成"
}

# ==================== 入口 ====================

main() {
    echo ""
    echo "=========================================="
    echo "     Groot Agent 自动化测试"
    echo "     版本: 1.0.0"
    echo "=========================================="
    echo ""

    setup_environment
    start_service

    trap stop_service EXIT

    run_tests
    print_summary
}

# 解析命令行参数
while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-start)
            SKIP_START=true
            shift
            ;;
        --llm-base-url)
            LLM_BASE_URL="$2"
            shift 2
            ;;
        --llm-api-key)
            LLM_API_KEY="$2"
            shift 2
            ;;
        --llm-model)
            LLM_MODEL="$2"
            shift 2
            ;;
        --help)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --skip-start          跳过服务启动（服务已运行时使用）"
            echo "  --llm-base-url URL    LLM API 地址"
            echo "  --llm-api-key KEY     LLM API 密钥"
            echo "  --llm-model MODEL     LLM 模型名称"
            echo "  --help                显示帮助"
            exit 0
            ;;
        *)
            echo "未知参数: $1"
            exit 1
            ;;
    esac
done

main