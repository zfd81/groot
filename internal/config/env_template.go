package config

// GenerateEnvTemplate 返回 ~/.groot/env.yaml 的初始模板。
//
// 设计要求：内容全注释，用户取消注释并填值即可启用数据库；
// 如保持注释或删除整个文件，则 cfg.Database 为 nil。
//
// 注意：database 子节使用"先缩进后 #"格式（如 "#  driver:"），用户删掉行首
// # 后 yaml 缩进自动正确。新增字段请遵循同样格式。
func GenerateEnvTemplate() string {
	return `# Groot 基础设施环境配置
# 存放数据库等外部服务的连接凭据，与业务配置 (config.yaml) 解耦。
#
# 默认情况下整个文件为注释（cfg.Database == nil）。
# 如需启用数据库后端，取消下方 database 块的注释并填入连接信息：
#   - 删除整个文件 → cfg.Database 为 nil
#   - 删除 database 节（或保持注释）→ cfg.Database 为 nil
#   - 完整填写 database 节 → 启用数据库

#database:
#  driver: sqlite                       # "sqlite" | "mysql" | "postgres"
#  dsn: ${DB_DSN}                       # 连接字符串（建议使用环境变量）
#  max_open_conns: 20                   # 最大打开连接数
#  max_idle_conns: 5                    # 最大空闲连接数
#  conn_max_lifetime: 30m               # 连接最大生命周期
`
}
