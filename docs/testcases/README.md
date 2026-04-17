# Groot Agent 测试执行指南

## 快速开始

### 1. 配置测试环境

编辑 `~/.groot-test/config.yaml` 或使用环境变量：

```bash
export GROOT_TEST_HOME=~/.groot-test
export GROOT_LLM_BASE_URL="http://127.0.0.1:8230/v1"
export GROOT_LLM_API_KEY="your-api-key"
export GROOT_LLM_MODEL="Qwen3.5-122B-A10B-6bit"
```

### 2. 运行测试

**方式一：完整测试（自动启动服务）**

```bash
./docs/testcases/test-runner.sh \
  --llm-base-url "http://127.0.0.1:8230/v1" \
  --llm-api-key "your-api-key" \
  --llm-model "Qwen3.5-122B-A10B-6bit"
```

**方式二：跳过服务启动（服务已运行）**

```bash
./docs/testcases/test-runner.sh --skip-start
```

### 3. 查看测试报告

测试完成后会显示：

```
==========================================
          测试结果汇总
==========================================

通过: 15
失败: 0
跳过: 3
总计: 18
```

---

## 测试用例分类

| 分类 | 用例数 | 说明 |
|------|--------|------|
| API 端点测试 | 22 | 测试所有 REST API |
| SSE 事件测试 | 4 | 测试 SSE 流响应格式 |
| 核心功能测试 | 3 | 测试 Agent 执行能力 |
| Skills 功能测试 | 4 | 测试 Skills 热插拔 |
| MCP 功能测试 | 3 | 测试 MCP 热插拔 |
| 存储功能测试 | 3 | 测试任务持久化 |
| 认证功能测试 | 3 | 测试认证拦截 |
| 性能测试 | 3 | 测试响应时间 |
| 边界测试 | 3 | 测试边界场景 |
| **总计** | **48** | |

---

## 测试文件结构

```
docs/testcases/
├── test-spec.md      # 测试用例规范文档
├── test-runner.sh    # 自动化测试脚本
└── README.md         # 本文件
```

---

## 单独测试某个功能

### 测试健康检查

```bash
curl -s http://localhost:8080/health | jq .
```

### 测试任务执行

```bash
curl -X POST http://localhost:8080/task/execute \
  -H "Content-Type: application/json" \
  -d '{"instruction": "你好"}'
```

### 测试 Skills 热插拔

```bash
# 添加 Skill
mkdir -p ~/.groot-test/skills/test_skill
cat > ~/.groot-test/skills/test_skill/SKILL.md << 'EOF'
---
name: test_skill
description: "测试技能"
---
# 测试技能指令
EOF

sleep 3
curl -s http://localhost:8080/skills | jq .

# 删除 Skill
rm -rf ~/.groot-test/skills/test_skill
sleep 3
curl -s http://localhost:8080/skills | jq .
```

### 测试 MCP 热插拔

```bash
# 添加 MCP
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

# 删除 MCP
rm ~/.groot-test/mcp/test_mcp.json
sleep 3
curl -s http://localhost:8080/tools | jq .
```

---

## 常见问题

### Q: 测试跳过 LLM 相关测试？

检查环境变量配置：
```bash
echo $GROOT_LLM_API_KEY
echo $GROOT_LLM_MODEL
```

### Q: 测试失败 "服务启动超时"？

1. 检查端口是否被占用：
   ```bash
   lsof -i :8080
   ```
2. 检查 groot 程序是否可执行：
   ```bash
   ./groot -H ~/.groot-test
   ```

### Q: 测试失败 "工具调用失败"？

检查 MCP 配置中的路径限制：
```yaml
restrictions:
  allowed_paths: ["~/.groot-test"]
```

---

## 添加新测试用例

1. 在 `test-spec.md` 中添加测试用例描述
2. 在 `test-runner.sh` 中添加测试函数
3. 在 `run_tests()` 函数中调用新测试

示例：

```bash
test_api_new_feature() {
    local tc_id="TC-API-XXX"
    log_info "测试 $tc_id: 新功能测试"

    local response
    response=$(curl -s http://localhost:$GROOT_PORT/new-endpoint)

    if [[ "$(echo "$response" | jq -r '.status')" == "success" ]]; then
        log_pass "$tc_id: 新功能正常"
    else
        log_fail "$tc_id: 新功能异常"
    fi
}
```

---

## 版本更新时测试

修改程序后，建议：

1. 运行完整测试套件
2. 检查失败测试
3. 修复问题后重新运行
4. 更新测试用例文档（如有新功能）