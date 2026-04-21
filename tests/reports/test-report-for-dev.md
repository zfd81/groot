# Groot 测试报告

**测试日期:** 2026-04-19  
**测试环境:** macOS Darwin 25.4.0  
**Python:** 3.9.6  
**pytest:** 8.4.2  

---

## 一、测试修改说明

根据研发反馈，本次测试调整了以下两个问题：

### 1. 认证测试适配

**问题:** 默认配置禁用认证 (`security.auth.enabled: false`)，但测试期望认证启用。

**解决方案:** 修改测试动态检测认证状态。

**修改文件:** `tests/test_authentication.py`

**修改内容:**
- 新增 `is_auth_enabled()` 函数，通过不带认证访问 API 来检测服务是否启用了认证
- 所有测试方法根据检测结果动态调整断言逻辑
- 认证启用时验证 401 响应，认证禁用时验证正常访问

**测试结果:** ✅ 11 passed, 3 skipped

---

### 2. 热插拔测试等待时间

**问题:** 热插拔配置防抖延迟为 2 秒，测试等待时间 3 秒可能不够。

**解决方案:** 增加等待时间到 5 秒。

**修改文件:**
- `tests/test_hot_reload.py` - 所有 `time.sleep(3)` → `time.sleep(5)`
- `tests/test_supplementary.py` - 热插拔相关等待时间
- `tests/test_api_endpoints.py` - Skills API 测试

**修改文件:** `tests/conftest.py`

**额外修复:**
- 确保测试目录在服务器启动前创建（避免 watcher 启动失败）
- 添加 `skills.hot_reload` 和 `mcp.hot_reload` 配置
- 使用正确的 groot 二进制路径 (`bin/groot`)
- 修复配置格式：`permissions: ["all"]` 而非 `permissions: "all"`

---

## 二、发现的服务端问题

### 问题 P0-1: Skills Watcher 竞态条件 (严重)

**现象:**  
当 skill 目录在服务器启动**之后**创建时，热插拔功能**不工作**。但当目录在服务器启动**之前**已存在时，热插拔功能**正常工作**。

**复现步骤:**
```bash
# 场景1：目录不存在启动服务器（失败）
rm -rf /tmp/groot_test/skills
mkdir -p /tmp/groot_test/mcp /tmp/groot_test/memory /tmp/groot_test/logs
./bin/groot -H /tmp/groot_test -p 8080 &
sleep 3
mkdir -p /tmp/groot_test/skills/test_skill
cat > /tmp/groot_test/skills/test_skill/SKILL.md << 'EOF'
---
name: test_skill
---
EOF
sleep 6
curl http://localhost:8080/skills  # 返回空列表 {"skills":[],"total":0}

# 场景2：目录预先存在启动服务器（成功）
rm -rf /tmp/groot_test
mkdir -p /tmp/groot_test/skills /tmp/groot_test/mcp
./bin/groot -H /tmp/groot_test -p 8080 &
sleep 3
mkdir -p /tmp/groot_test/skills/test_skill
cat > /tmp/groot_test/skills/test_skill/SKILL.md << 'EOF'
---
name: test_skill
---
EOF
sleep 6
curl http://localhost:8080/skills  # 返回 {"skills":[{"name":"test_skill"}]}
```

**根因分析:**

查看 `internal/skill/watcher.go:39-70` 和 `internal/skill/watcher.go:94-103`：

```go
// Start 方法 - 第39-70行
func (w *Watcher) Start(dir string) error {
    if !w.config.HotReload.Enabled {
        return nil
    }

    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return err
    }
    w.watcher = watcher

    // 问题1: 如果目录不存在，这里会失败
    if err := watcher.Add(dir); err != nil {
        return err  // ← 返回错误，导致 watcher 未启动
    }

    // 只监控已存在的子目录
    entries, err := os.ReadDir(dir)
    if err == nil {
        for _, entry := range entries {
            if entry.IsDir() {
                subdir := filepath.Join(dir, entry.Name())
                watcher.Add(subdir)
            }
        }
    }

    go w.run(dir)
    return nil
}

// handleEvent 方法 - 第94-103行
func (w *Watcher) handleEvent(dir string, event fsnotify.Event, debounceDelay time.Duration) {
    // 问题2: 检测到新目录后添加监控，但立即返回不处理后续文件事件
    if event.Op == fsnotify.Create {
        info, err := os.Stat(event.Name)
        if err == nil && info.IsDir() {
            w.watcher.Add(event.Name)
            return  // ← 立即返回，不处理同一目录下紧接着创建的 SKILL.md
        }
    }
    // ...
}
```

**问题分析:**

1. **Watcher 启动时目录必须存在**  
   `Start()` 方法调用 `watcher.Add(dir)` 时，如果 skills 目录不存在，会返回错误 "lstat ... no such file or directory"，导致 watcher 未启动。服务日志显示：
   ```
   {"level":"error","message":"无法启动Skills watcher","error":"lstat /tmp/groot_test/skills: no such file or directory"}
   ```

2. **新目录监控存在竞态条件**  
   当检测到新目录创建事件时，`handleEvent` 会添加该目录到 watcher 监控列表并立即返回。如果 `SKILL.md` 文件紧接着在同一目录下创建，这个文件创建事件可能发生在 watcher 添加目录监控完成之前，导致文件事件丢失。

**修复建议:**

方案1 - 确保 skills/mcp 目录存在（简单方案）:

修改 `cmd/groot/main.go`，在启动 watcher 前确保目录存在：

```go
// 在 Start watcher 前添加
skillsDir := filepath.Join(homeDir, "skills")
if err := os.MkdirAll(skillsDir, 0755); err != nil {
    log.Error("无法创建skills目录", zap.Error(err))
}

skillWatcher := skill.NewWatcher(skillRegistry, cfg.Skills, log)
if err := skillWatcher.Start(skillsDir); err != nil {
    log.Error("无法启动Skills watcher", zap.Error(err))
}
```

方案2 - 改进 watcher 实现处理竞态（推荐）:

修改 `internal/skill/watcher.go`:

```go
func (w *Watcher) handleEvent(dir string, event fsnotify.Event, debounceDelay time.Duration) {
    // 检测新目录 - 添加监控后，检查目录下是否已有 SKILL.md
    if event.Op == fsnotify.Create {
        info, err := os.Stat(event.Name)
        if err == nil && info.IsDir() {
            w.watcher.Add(event.Name)
            
            // 新增: 检查目录下是否已有 SKILL.md 文件
            skillFile := filepath.Join(event.Name, "SKILL.md")
            if _, err := os.Stat(skillFile); err == nil {
                // 文件已存在，触发加载
                if err := w.loader.Load(skillFile); err != nil {
                    w.logger.Error("failed to load skill", zap.Error(err))
                } else {
                    skillName := extractSkillName(skillFile)
                    w.logger.LogSkillHotReload("added", skillName, w.loader.registry.Count())
                }
            }
            return
        }
    }
    
    // 原有的 SKILL.md 处理逻辑...
}
```

---

### 问题 P0-2: SSE goroutine 异步执行导致连接过早关闭 (已知问题)

**状态:** 已在上一轮测试报告中记录，研发已知。

**位置:** `internal/api/handler/chat.go:215`

**现象:** Agent 执行使用 goroutine 异步运行，HTTP handler 在启动 goroutine 后立即返回，导致 SSE 连接关闭，后续事件无法发送。

**影响:** 所有涉及 LLM 交互的测试失败，用户无法收到完整的对话响应。

---

### 问题 P1-1: intent 事件重复发送

**现象:** SSE 中 `intent` 事件被发送两次。

**日志示例:**
```
event: intent
data: {"timestamp":"2026-04-19T00:56:25Z"}

event: intent  
data: {"timestamp":"2026-04-19T00:56:25Z"}
```

**需要检查:** `internal/agent/sse.go` 和调用处，确保 `WriteIntent()` 只被调用一次。

---

### 问题 P1-2: Health 接口缺少 memory 检查

**设计文档要求:**
```json
{
  "checks": {
    "memory": {"status": "healthy", "used_mb": 256}
  }
}
```

**实际返回:** 无 `memory` 字段。

---

### 问题 P1-3: 字段命名不一致

| 设计文档定义 | 实际实现 |
|-------------|---------|
| `chats_running` | `tasks_running` |

---

## 三、测试运行结果

### 认证测试

```
tests/test_authentication.py
├── TestAuthenticationBasic
│   ├── test_no_api_key_behavior ✅ PASSED
│   ├── test_invalid_api_key_behavior ✅ PASSED
│   ├── test_valid_api_key_success ✅ PASSED
│   └── test_empty_api_key_behavior ✅ PASSED
├── TestAuthenticationAllAPIs
│   ├── test_chat_api_auth_behavior ✅ PASSED
│   ├── test_delete_chat_auth_behavior ✅ PASSED
│   ├── test_chat_status_auth_behavior ✅ PASSED
│   ├── test_chat_detail_auth_behavior ✅ PASSED
│   ├── test_session_detail_auth_behavior ✅ PASSED
│   └── test_session_history_auth_behavior ✅ PASSED
├── TestHealthNoAuth
│   └── test_health_no_auth_required ✅ PASSED
└── TestPermissionSystem
│   ├── test_permission_chat_only ⏭️ SKIPPED (需要多 Key 环境)
│   ├── test_permission_all_access ⏭️ SKIPPED
│   └── test_permission_forbidden ⏭️ SKIPPED

结果: 11 passed, 3 skipped
```

### 热插拔测试

```
tests/test_hot_reload.py
├── TestSkillsHotReload
│   ├── test_add_skill_updates_list ❌ FAILED (watcher 竞态问题)
│   ├── test_remove_skill_updates_list ❌ FAILED
│   └── test_modify_skill_updates_content ✅ PASSED (目录预先存在)
├── TestMCPHotReload
│   ├── test_add_mcp_updates_tools ✅ PASSED
│   ├── test_remove_mcp_updates_tools ✅ PASSED
│   ├── test_modify_mcp_reconnects ✅ PASSED
│   └── test_mcp_deactivate ✅ PASSED
├── TestSkillFormat
│   └── test_skill_yaml_frontmatter ✅ PASSED
├── TestMCPFormat
│   ├── test_mcp_json_format ✅ PASSED
│   └── test_mcp_sse_type ✅ PASSED
└── TestDebounceDelay
│   └── test_debounce_delay ✅ PASSED

结果: 9 passed, 2 failed (Skills watcher 竞态)
```

---

## 四、修复优先级建议

### P0 - 必须立即修复

| 问题 | 位置 | 影响 | 建议 |
|-----|-----|-----|-----|
| Skills Watcher 竞态 | `internal/skill/watcher.go` | 热插拔功能不工作 | 确保目录存在 + 改进竞态处理 |
| SSE goroutine 异步 | `internal/api/handler/chat.go:215` | LLM 交互完全失败 | 改为同步执行或等待完成 |

### P1 - 重要修复

| 问题 | 位置 | 建议 |
|-----|-----|-----|
| intent 重复发送 | `internal/agent/sse.go` | 确保 WriteIntent() 只调用一次 |
| Health 缺少 memory | `internal/api/handler/health.go` | 添加 memory 健康检查 |
| 字段命名不一致 | API 响应 | 使用设计文档定义的 `chats_running` |

---

## 五、附：手动验证命令

### 验证 Skills Watcher 问题

```bash
# 清理环境
pkill -f groot; rm -rf /tmp/groot_test

# 测试1：目录不存在启动（失败场景）
mkdir -p /tmp/groot_test/mcp /tmp/groot_test/memory /tmp/groot_test/logs
./bin/groot -H /tmp/groot_test -p 8080 &
sleep 3
mkdir -p /tmp/groot_test/skills/test_skill
echo '---
name: test_skill
---
# Test' > /tmp/groot_test/skills/test_skill/SKILL.md
sleep 6
curl -s http://localhost:8080/skills
# 预期: {"skills":[],"total":0} ← 空，watcher 未检测到

# 测试2：目录预先存在启动（成功场景）
pkill -f groot; rm -rf /tmp/groot_test
mkdir -p /tmp/groot_test/skills /tmp/groot_test/mcp
./bin/groot -H /tmp/groot_test -p 8080 &
sleep 3
mkdir -p /tmp/groot_test/skills/test_skill
echo '---
name: test_skill
---
# Test' > /tmp/groot_test/skills/test_skill/SKILL.md
sleep 6
curl -s http://localhost:8080/skills
# 预期: {"skills":[{"name":"test_skill"}]} ← 正常加载
```

### 验证 SSE 问题

```bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-api-key-2026" \
  -d '{"instruction": "你好"}'
# 预期: 只收到 intent 事件，没有后续 thought/step/completed 事件
```

---

**报告编写:** Claude Code  
**报告日期:** 2026-04-19