# API 工具配置设计

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 groot 添加 API 工具能力，通过 JSON 配置文件定义 HTTP API 调用，自动转换为 eino 框架的工具，与 MCP 工具并存。

**Architecture:** 创建独立的 api 包处理 API 工具的配置加载、验证、HTTP 请求执行；通过 APIToolAdapter 适配到 eino 的 InvokableTool 接口；启动时检查环境变量和命名冲突；统一注册到工具表。

**Tech Stack:** Go, eino framework, HTTP client, JSON 配置

---

## 背景

groot 现有 MCP 工具能力，通过 MCP 协议（stdio/sse/http）调用外部工具。但很多场景只需要简单的 HTTP API 调用，不需要 MCP 协议的复杂性。API 工具作为 MCP 的补充，提供更直接的 HTTP API 集成方式。

**两种工具类型对比：**

| 特性 | MCP 工具 | API 工具 |
|------|----------|-----------|
| 配置位置 | `~/.groot/mcp/*.json` | `~/.groot/api/*.json` |
| 执行方式 | MCP 协议（stdio/sse/http） | 直接 HTTP 请求 |
| 适用场景 | 复杂交互、外部进程、标准化工具 | 简单 API 调用、已有 HTTP 服务 |

---

## 目录结构

```
~/.groot/
├── config.yaml          # 系统配置
├── mcp/                 # MCP 工具配置目录
│   ├── filesystem.json
│   └── web-search.json
├── api/                 # API 工具配置目录
│   ├── get_weather.json
│   ├── create_order.json
├── memory/              # Memory 数据目录
├── skills/              # Skills 目录
├── groot.md             # GROOT.md 指导文件
└── logs/                # 日志目录
```

---

## 配置格式

### 基本结构

```json
{
  "name": "get_weather",
  "description": "获取天气信息",

  "url": "https://api.weather.com/v1/weather/${city}",
  "method": "GET",

  "auth": {
    "type": "bearer",
    "token": "$${WEATHER_API_KEY}"
  },

  "headers": {
    "Content-Type": "application/json",
    "X-Request-ID": "${requestId}"
  },

  "query": {
    "unit": "${unit}"
  },

  "body": {
    "name": "${name}",
    "email": "${email}"
  },

  "bodyType": "json",

  "timeout": 30,

  "parameters": [
    {
      "name": "city",
      "type": "string",
      "required": true,
      "description": "城市名称"
    },
    {
      "name": "unit",
      "type": "string",
      "required": false,
      "default": "celsius",
      "description": "温度单位"
    }
  ]
}
```

### 字段定义

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | ✓ | 工具名称，eino注册时使用，需唯一 |
| `description` | string | ✓ | 工具描述，LLM决策时使用 |
| `url` | string | ✓ | 完整请求URL，支持 `${参数}` 和 `$${环境变量}` |
| `method` | string | ✓ | HTTP方法：GET/POST/PUT/DELETE/PATCH |
| `auth` | object | | 认证配置，自动注入到请求头 |
| `headers` | object | | 自定义请求头，值支持固定值、`${参数}`、`$${环境变量}` |
| `query` | object | | URL查询参数，拼接到URL后面，值支持固定值、`${参数}`、`$${环境变量}` |
| `body` | object | | 请求体内容，值支持固定值、`${参数}`、`$${环境变量}`，支持嵌套结构 |
| `bodyType` | string | | 请求体格式：json/form（POST/PUT/PATCH时使用） |
| `timeout` | int | | 超时秒数，默认30 |
| `parameters` | array | ✓ | 工具参数列表，对应eino的InputSchema |

### 认证配置

| 字段 | 说明 |
|------|------|
| `auth.type` | 认证类型：bearer/basic/apikey/none |
| `auth.token` | Bearer认证Token，支持 `$${环境变量}` |
| `auth.username` | Basic认证用户名（仅basic类型） |
| `auth.password` | Basic认证密码，支持 `$${环境变量}`（仅basic类型） |
| `auth.key` | ApiKey密钥值，支持 `$${环境变量}`（仅apikey类型） |
| `auth.location` | ApiKey注入位置：header/query（仅apikey类型） |
| `auth.name` | ApiKey的header名或query参数名（仅apikey类型） |

### 认证类型说明

| auth.type | 自动注入内容 |
|-----------|--------------|
| `bearer` | Header: `Authorization: Bearer <token>` |
| `basic` | Header: `Authorization: Basic <base64(username:password)>` |
| `apikey` | 根据location注入到header或query |
| `none` | 不注入认证信息 |

### 参数定义

| 字段 | 说明 |
|------|------|
| `parameters[].name` | 参数名称 |
| `parameters[].type` | 参数类型：string/int/float/bool/array/object |
| `parameters[].required` | 是否必须有值（传入值或默认值） |
| `parameters[].default` | 默认值 |
| `parameters[].description` | 参数描述，LLM决策时使用 |

---

## 变量语法

| 语法 | 来源 | 示例 |
|------|------|------|
| `${参数名}` | 工具调用参数，用户调用时传入 | `${city}` → 用户传入的city参数值 |
| `$${环境变量名}` | 系统环境变量 | `$${WEATHER_API_KEY}` → 系统环境变量WEATHER_API_KEY的值 |

---

## 参数处理逻辑

`required: true` 表示调用 API 时该参数必须有值，值的来源可以是用户传入或默认值。

| required | 有默认值 | 用户传入 | 结果 |
|----------|----------|----------|------|
| true | 有 | ✓ | 使用传入值 |
| true | 有 | ✗ | 使用默认值 |
| true | 无 | ✓ | 使用传入值 |
| true | 无 | ✗ | **报错**（必须有值但没有） |
| false | 有 | ✓ | 使用传入值 |
| false | 有 | ✗ | 使用默认值 |
| false | 无 | ✓ | 使用传入值 |
| false | 无 | ✗ | 参数为空（可选参数） |

**简化理解：**
- `required: true` → 必须有值（传入或默认值都行）
- `required: false` → 可选（有值用值，没值空着也行）

---

## 请求体格式

| bodyType | Content-Type | 结构特点 |
|----------|--------------|----------|
| `json` | `application/json` | 支持嵌套对象和数组 |
| `form` | `application/x-www-form-urlencoded` | 仅支持扁平结构 |

---

## 启动检查

### 环境变量检查

系统启动时，扫描所有 API 工具配置，检查 `$${环境变量}` 引用的环境变量是否存在：
- 不存在 → 报错：`环境变量 WEATHER_API_KEY 未设置，工具 get_weather 无法加载`
- 系统不启动

### 工具命名冲突检查

系统启动时，收集所有工具名称（MCP 工具 + API 工具）：
- 存在同名 → 报错：`工具名称冲突: get_weather 在 MCP 和 API 工具中都定义了`
- 系统不启动

---

## 返回行为

| 场景 | 返回内容 |
|------|----------|
| HTTP 成功（状态码 200-299） | 直接返回整个 response body 的 string |
| HTTP 失败（状态码非 200-299） | 返回：`HTTP错误: 状态码XXX, 响应内容: {...}` |

LLM 不需要预定义返回值结构，它会直接理解返回的 string 内容。

---

## 错误处理

| 错误类型 | 返回内容 |
|----------|----------|
| 网络错误 | `网络请求失败: xxx` |
| 超时 | `请求超时（xxx秒）` |
| HTTP 状态码非 200-299 | `HTTP错误: 状态码XXX, 响应内容: {...}` |
| 参数缺失（调用时） | `缺少必填参数: xxx` |

---

## 完整示例

### GET 请求

```json
{
  "name": "get_weather",
  "description": "获取天气信息",

  "url": "https://api.weather.com/v1/weather/${city}",
  "method": "GET",

  "auth": {
    "type": "bearer",
    "token": "$${WEATHER_API_KEY}"
  },

  "query": {
    "unit": "${unit}"
  },

  "timeout": 30,

  "parameters": [
    {"name": "city", "type": "string", "required": true, "description": "城市名称"},
    {"name": "unit", "type": "string", "required": false, "default": "celsius", "description": "温度单位"}
  ]
}
```

### POST 请求（JSON body 嵌套）

```json
{
  "name": "create_order",
  "description": "创建订单",

  "url": "https://api.example.com/v1/orders",
  "method": "POST",

  "auth": {
    "type": "bearer",
    "token": "$${API_TOKEN}"
  },

  "headers": {
    "Content-Type": "application/json"
  },

  "body": {
    "orderId": "${orderId}",
    "customer": {
      "name": "${customerName}",
      "phone": "${customerPhone}",
      "address": {
        "province": "${province}",
        "city": "${city}"
      }
    },
    "items": [
      {"productId": "${productId}", "quantity": "${quantity}"}
    ]
  },

  "bodyType": "json",

  "timeout": 30,

  "parameters": [
    {"name": "orderId", "type": "string", "required": true, "description": "订单ID"},
    {"name": "customerName", "type": "string", "required": true, "description": "客户姓名"},
    {"name": "customerPhone", "type": "string", "required": true, "description": "客户电话"},
    {"name": "province", "type": "string", "required": true, "description": "省份"},
    {"name": "city", "type": "string", "required": true, "description": "城市"},
    {"name": "productId", "type": "string", "required": true, "description": "商品ID"},
    {"name": "quantity", "type": "int", "required": true, "description": "数量"}
  ]
}
```

### POST 请求（form body）

```json
{
  "name": "submit_form",
  "description": "提交表单",

  "url": "https://api.example.com/v1/forms",
  "method": "POST",

  "auth": {
    "type": "apikey",
    "key": "$${FORM_API_KEY}",
    "location": "header",
    "name": "X-API-Key"
  },

  "body": {
    "username": "${username}",
    "password": "${password}",
    "remember": "${remember}"
  },

  "bodyType": "form",

  "timeout": 30,

  "parameters": [
    {"name": "username", "type": "string", "required": true, "description": "用户名"},
    {"name": "password", "type": "string", "required": true, "description": "密码"},
    {"name": "remember", "type": "bool", "required": false, "default": false, "description": "记住登录"}
  ]
}
```

---

## 技术实现要点

### 1. 配置加载

- 创建 `internal/api` 包（注意：与现有的 `internal/api` 目录区分，现有的是 API Server）
- 由于已有 `internal/api` 目录（HTTP 服务），需要考虑命名：
  - 方案A：在现有 `internal/api` 包中添加 API 工具相关代码
  - 方案B：使用新目录名如 `internal/apitool` 或 `internal/resttool`
- 加载 `~/.groot/api/*.json` 配置文件
- 解析配置结构

### 2. 启动检查

- 遍历所有配置，提取 `$${环境变量}` 引用
- 检查环境变量是否存在
- 收集所有工具名称，与 MCP 工具名称对比，检查冲突

### 3. 工具适配

- 创建 `APIToolAdapter` 结构，实现 eino 的 `InvokableTool` 接口
- `Info()` 方法返回工具元信息（name, description, parameters）
- `InvokableRun()` 方法执行 HTTP 请求

### 4. HTTP 请求执行

- 变量替换：`${参数}` → 参数值，`$${环境变量}` → 环境变量值
- 参数验证：检查必填参数是否有值
- 构建 HTTP 请求：URL、headers、query、body
- 认证注入：根据 auth.type 自动添加认证信息
- 执行请求并返回结果

### 5. 工具注册

- 在系统启动时，加载 API 工具配置
- 创建 Adapter 并注册到统一的工具表
- 与 MCP 工具共享同一个注册机制

---

## 与现有 MCP 架构的关系

| 层面 | MCP 工具 | API 工具 |
|------|----------|-----------|
| 配置文件 | `mcp/*.json` | `api/*.json` |
| 加载包 | `internal/mcp` | `internal/apitool`（或合并到现有包） |
| 适配器 | `MCPToolAdapter` | `APIToolAdapter` |
| 注册机制 | 统一注册到工具表 | 统一注册到工具表 |
| 执行方式 | MCP 协议 | 直接 HTTP |

两种工具类型独立配置、独立加载，但最终都转换为 eino 的 `InvokableTool` 注册到同一个工具表。