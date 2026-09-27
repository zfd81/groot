# 认证配置面板实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 设置面板新增「认证」分组：JWT 签名密钥脱敏显示并支持一键重新生成，API Key 请求头名可手工设置；由此消除"配置项既不在 yaml 也不在界面"的缺口。

**Architecture:** 后端在 `/web/settings/auth` 提供 GET（回读，密钥脱敏）与 PUT（保存请求头名），`POST /web/settings/auth/secret` 重新生成密钥。setting 层新增 `RegenerateAuthSecret`；`SetAuthHeaderName` 已有，空串语义定为"恢复默认"（删行）。两项改动均需重启生效，界面明确标注；重新生成密钥带二次确认（所有已签发 API Key 立即失效）。密钥明文永不回传，脱敏规则复用发送渠道 SMTP 密码的规则（`****` + 尾四位）。

**Tech Stack:** Go（Hertz handler + 既有 setting 包）、Vue 3 + Element Plus、i18n 双语言。

**约束**
- 遵循项目规范：先设计文档后代码；Go 单测配套；不做 git 操作（用户统一提交）。
- 校验：header_name 匹配 `^[A-Za-z0-9-]{1,64}$`（HTTP header 名安全子集），空串表示恢复默认 `X-API-Key`。
- GET 响应：`{header_name, header_name_default, secret_masked, secret_set}`；PUT 请求：`{header_name}`；POST 重新生成响应同 GET。

---

## Task A: 设计文档 + 后端（setting 层与 handler、路由、单测）

**Files:**
- Create: `docs/superpowers/specs/2026-09-27-auth-settings-panel-design.md`
- Modify: `docs/superpowers/specs/2026-09-27-bootstrap-config-design.md`（1.5 节"不经界面暴露"已过时，改为"经认证分组管理"；迭代说明追加）
- Modify: `internal/setting/auth.go`（新增 `RegenerateAuthSecret`；`SetAuthHeaderName` 空串→删行）
- Modify: `internal/api/handler/setting.go`（或新建 `internal/api/handler/auth_setting.go`）
- Modify: `internal/api/types/types.go`、`internal/api/router.go`
- Test: `internal/setting/auth_test.go`、`internal/api/handler/setting_test.go`（或新 handler 测试文件）

要点：
- `RegenerateAuthSecret(ctx) (string, error)`：无条件 `GenerateAuthSecret` + Upsert，返回新密钥（仅供脱敏展示用）。
- `SetAuthHeaderName(ctx, "")` → `repo.Delete` 该键（恢复默认）；非空时先校验格式。
- handler：GET 用 `Settings.Auth` 回读、密钥用与 SMTP 密码同一脱敏函数；PUT 校验后写表；POST 重新生成后返回新脱敏值。三个接口均在响应中不含明文密钥。
- 单测覆盖：Regenerate 每次生成新值且落表；SetAuthHeaderName 空串删行恢复默认、非法格式拒绝；handler GET 脱敏且不含明文、PUT 往返、PUT 非法 400、POST 后 GET 回读变化。

## Task B: 前端（认证分组）+ 文档对齐

**Files:**
- Create: `web/src/api/authSettings.ts`（类型 + 三个请求函数）
- Modify: `web/src/components/settings/SettingsModal.vue`（配置分区新增「认证」分组，放在「限流」之后）
- Modify: `web/src/i18n/messages/zh-cn.ts`、`en.ts`
- Modify: `README.md`（4.3 Security 说明改为面板管理，升级章口径核对）、`tests/TEST_CASES.md`（新增接口用例点）

要点：
- header_name 输入框：placeholder 显示默认值，改动即保存（对齐面板既有交互），说明文字标注「重启后生效」；清空保存即恢复默认。
- secret 行：显示脱敏值 + 「重新生成」按钮；点击弹 ElMessageBox.confirm，文案警告"所有已签发的 API Key 将立即失效，重启后生效"；成功后刷新脱敏值并 ElMessage 提示。
- 构建验证 `npm run build`。

## 验证

- `go build ./... && go test ./internal/setting/ ./internal/api/handler/ -count=1` 全绿
- `cd web && npm run build` 成功
