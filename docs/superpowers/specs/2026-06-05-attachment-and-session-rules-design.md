# 附件访问与会话规则设计

**日期**:2026-06-05
**状态**:设计稿

---

## 一、功能设计

### 1.1 功能概述

Groot 为 LLM 提供两个内置工具,使其能够主动列出和读取当前会话上传的附件,并通过嵌入式会话规则常量告诉模型:何时用内置工具、何时用文件系统 MCP 工具。

核心要点:

- 两个内置工具 `groot_file_list` / `groot_file_read`,作为请求级工具实例,持有启动期实例化的 `storage.Storage` 与本次执行的 sessionID
- 存储后端(local / minio)在 groot 启动期由 `storage.New(cfg.Storage)` 选定,运行期不再切换;工具直接调用接口方法,不感知后端类型
- 会话规则通过 `//go:embed` 嵌入二进制,作为常量直接返回

### 1.2 内置工具

#### 1.2.1 工具定义

| 工具 | 入参 | 返回 | 用途 |
|------|------|------|------|
| `groot_file_list` | 无 | Markdown 表格(文件名 / 大小 / 上传时间) | 列出当前会话所有附件 |
| `groot_file_read` | `filename`(只接受文件名,不接受路径) | 文件内容(文本直出,二进制 base64) | 按文件名读取附件内容 |

#### 1.2.2 工具元信息(LLM 视角)

LLM 通过 `schema.ToolInfo` 看到工具描述,据此决定何时调用。Desc 遵循以下原则:

- **只描述"工具是什么、做什么、返回什么"**,不写"何时调用"的使用规则
- "何时调用"统一由 `defaultSessionRules`(见 §1.4)提供,避免双源真相
- 措辞精炼,不举例(例子对 LLM 是噪声),不包含可能引发幻觉的修饰

`groot_file_list`:

```go
&schema.ToolInfo{
    Name: "groot_file_list",
    Desc: "列出当前会话的附件清单,返回 Markdown 表格(列:文件名 / 大小 / 上传时间);无附件时返回文本提示。",
    ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{}),
}
```

`groot_file_read`:

```go
&schema.ToolInfo{
    Name: "groot_file_read",
    Desc: "按文件名读取当前会话的附件内容:文本文件返回 UTF-8 原文,二进制文件返回 base64 字符串。",
    ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
        "filename": {
            Desc:     "附件文件名,不含路径分隔符(/、\\)和路径回溯(..)",
            Required: true,
            Type:     schema.String,
        },
    }),
}
```

#### 1.2.3 内部实现与路径拼接

**Storage 实例的获取方式**:groot 启动期 `cmd/groot/main.go` 调用 `storage.New(cfg.Storage)` **一次性**构造一个 `storage.Storage` 实例(进程级单例),然后通过依赖注入链路传递给需要它的模块——目前已传给 `memory.Manager`,本次新增传给 `agent.Executor`。`storage.Storage` 是接口,后续所有持有者拿到的都是**同一个实例的引用**,不会重复构造、不会重连后端。

工具结构体持有三个字段:

- `storage` —— 由 `Executor` 通过字段引用透传,所有工具实例共享同一个对象
- `memoryDir` —— 启动期确定的记忆目录根
- `sessionID` —— 本次 Execute 绑定的会话 ID(请求级,每次新建工具实例时写入)

每次 `Executor.Execute` 只新建"工具结构体"这个轻量壳子(几十字节),`storage` 字段是接口指针赋值,**不涉及 storage 后端的重新初始化**。这与 `CallAgentTool` 每次新建实例但共享 `*memory.Manager` 是同一个模式。

**附件目录拼接**复用 `Manager` 的规则:

```go
attachmentsDir := manager.AttachmentsDir(sessionID)
// 等价于 filepath.Join(memoryDir, sessionID, "attachments")
```

工具内部直接调用 `storage.Storage` 接口,不感知后端是 local 还是 minio:

- `groot_file_list`:`storage.List(ctx, attachmentsDir)` 取 `[]*FileInfo`,过滤 `IsDir=true` 项,渲染为 Markdown 表格
- `groot_file_read`:校验 `filename` 合法后,`storage.Read(ctx, filepath.Join(attachmentsDir, filename))` 读取流并返回内容

为统一路径规则,`memory.Manager` 把原私有 `attachmentsDir` 升级为导出方法 `AttachmentsDir(sessionID string) string`,工具直接调用该方法,避免在工具层重复拼接逻辑。

#### 1.2.4 文本与二进制判定

按文件扩展名(小写)判定:

- **文本类**(直接返回 UTF-8 内容):`.txt` / `.md` / `.markdown` / `.json` / `.yaml` / `.yml` / `.toml` / `.xml` / `.html` / `.htm` / `.css` / `.csv` / `.tsv` / `.log` / `.ini` / `.conf` / `.go` / `.py` / `.js` / `.ts` / `.tsx` / `.jsx` / `.java` / `.c` / `.cpp` / `.h` / `.hpp` / `.rs` / `.rb` / `.php` / `.sh` / `.bash` / `.zsh` / `.sql`
- **其他扩展名**:base64 编码后返回

文件大小不在工具层做限制——上层(chat handler 上传环节、`storage` 实现自身)负责控制。session 上传时已经定义过处理模型,工具层只负责按需读取。

#### 1.2.5 上传时间语义

`groot_file_list` 表格的"上传时间"列直接对应 `storage.FileInfo.ModTime`:

- local 实现:对应文件系统 ModTime,等同上传写入时间
- minio 实现:对应对象的 LastModified

附件目录里的文件不会被运行时改写,因此 ModTime 与上传时间一致。

#### 1.2.6 调用边界与错误返回

- **session 边界**:每个工具实例通过闭包捕获一个 sessionID,LLM 无法跨会话访问其他 session 的附件
- **路径边界**:`filename` 拒绝路径分隔符(`/`、`\`)与路径回溯(`..`)
- **错误返回**:遵循 eino 工具错误标准——参数非法、文件不存在、底层 IO 异常等通过 `(string, error)` 返回值的 `error` 项呈现,LLM 收到错误后按 §1.3 决策树降级到文件系统 MCP 工具或告知用户

#### 1.2.7 注册方式

工具实例采用**请求级生命周期**——每次 `Executor.Execute` 在创建 engine 前现场构造一对 `*GrootFileListTool` / `*GrootFileReadTool`,把本次执行的 sessionID 作为字段写入实例。这与 `CallAgentTool` 的模式一致(参考 [internal/agent/call_agent.go](internal/agent/call_agent.go) 中 `CallAgentTool` 持有 `sessionID` 字段、`InvokableRun` 直接读字段),LLM 入参中不暴露 session 信息。

注入到 `extraTools` 的顺序固定为:`[groot_file_list, groot_file_read, call_agent]`(`call_agent` 仅在编排模式下加入)。三条执行路径都注入这两个内置工具:

- **主 Agent(编排模式)**:`Executor.Execute` 直接构造并加入 `extraTools`
- **Solo 子 Agent**:`Executor.Execute` 走 Solo 分支时同样构造并加入 `extraTools`
- **通过 `call_agent` 调度的子 Agent**:`SubAgentEntry.BuildAgentTool` 接受 storage + memoryDir + sessionID 参数,在构造子 Agent engine 时把这两个工具加入其 `extraTools`

### 1.3 工具选择规则(LLM 视角)

规则通过内置常量 `defaultSessionRules` 注入到每轮系统指令:

```
用户提到一个文件
  │
  ├─ 文件名含路径分隔符(/ 或 \)→ 优先用文件系统 MCP 工具
  │
  └─ 裸文件名(如 report.pdf)
        ├─ 调用 groot_file_list 查附件清单
        ├─ 命中 → 调用 groot_file_read 读取
        └─ 未命中 → 尝试文件系统 MCP 工具,都没有则告知未找到
```

### 1.4 会话规则常量

`defaultSessionRules` 通过 `//go:embed session_rules.md` 嵌入二进制,`Manager.GetSessionMdContent` 直接返回该常量。

规则全文:

```markdown
# 会话文件提示

当前会话的文件(用户上传的附件等)使用内置工具访问:
- `groot_file_list` —— 查看当前会话中的所有文件清单
- `groot_file_read filename="xxx"` —— 读取指定文件的内容(仅文件名,不含路径)

## 规则
1. 用户提到一个文件且只给了文件名(没有完整路径)时,先调用 `groot_file_list` 检查该文件是否在当前会话附件清单中
2. 在清单中:用 `groot_file_read filename="xxx"` 读取
3. 不在清单中:在当前可用工具中寻找文件系统读取类工具,有则使用,没有则告知用户该文件未找到
4. 用户给出明确路径(如 `/home/file.txt`)时,直接用文件系统 MCP 工具读取,不需要先查附件清单
5. 每次用户提到新文件名时都要重新调用 `groot_file_list` 检查清单,不要凭上一轮对话的印象直接判断文件是否存在——附件清单可能在会话中发生变化

## 说明
- `groot_file_read` 的 filename 参数只接受文件名,不接受路径(传入路径会调用失败)
- `groot_file_list` 和 `groot_file_read` 是内置工具,始终可用
- 文件系统类 MCP 工具由用户自行配置,可能不存在——工具列表中找不到时不要强行尝试
- 文件系统上的文件(代码文件、配置文件等)请使用对应的文件系统 MCP 工具
```

文件布局:

```
internal/memory/
├── session_rules.go        # 新增://go:embed session_rules.md
└── session_rules.md        # 新增:规则正文
```

### 1.5 Manager 改造

- `CreateSession`:删除写 `SESSION.md` 物理文件的代码块
- `GetSessionMdContent(sessionID string) (string, error)`:接口签名**保持不变**(为最小化改动并保持向后兼容);实现忽略 sessionID 参数,直接返回 `defaultSessionRules` 常量,err 永远为 nil。方法注释中说明:sessionID 仅作签名兼容保留,不参与逻辑
- 新增 `AttachmentsDir(sessionID string) string` 导出方法,把原私有 `attachmentsDir` 暴露给内置工具,避免路径规则在两处硬编码

#### 旧 SESSION.md 物理文件处理

升级前已经存在的 session 目录里可能残留 `SESSION.md` 物理文件:

- 新版 `GetSessionMdContent` 不再读取该物理文件,直接返回常量,因此旧文件**不影响功能**
- 不主动迁移、不主动删除旧文件;`Cleanup` 走 `os.RemoveAll(sessionDir)` 时会随会话过期一并清理

---
