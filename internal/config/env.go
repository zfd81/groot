package config

// EnvFileName 是历史版本基础设施环境配置文件（env.yaml）的固定文件名。
// 新版配置已由 bootstrap.yaml 承载，该常量仅服务于 MigrateLegacy 的
// 老配置迁移路径（读取遗留 env.yaml 并入 bootstrap.yaml）。
const EnvFileName = "env.yaml"
