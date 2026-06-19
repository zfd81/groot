package config

// GenerateEnvTemplate 返回 ~/.groot/env.yaml 的初始模板。
//
// 设计要求：内容全注释（cfg.Database == nil → SQLite 本地模式）。
// 模板为 MySQL 和 PostgreSQL 各提供一个完整示例块，二选一取消整块注释
// 即可启用对应驱动。SQLite 不出现在模板中（零配置即 SQLite，无需在
// env.yaml 中声明）。
//
// 注意：每个示例块均使用"先缩进后 #"格式（如 "#  driver:"），用户
// 删掉每行行首的 # 后 yaml 缩进自动正确。两个示例块同时取消注释会
// 因 yaml 中存在重复 key 而解析失败 —— 这是预期行为。
func GenerateEnvTemplate() string {
	return `# Groot 基础设施环境配置
# 存放数据库等外部服务的连接凭据，与业务配置 (config.yaml) 解耦。
#
# 默认情况下整个文件为注释（cfg.Database == nil），等价于 SQLite 本地
# 模式（数据库文件 ~/.groot/groot.db），无需任何配置。
#
# 启用 MySQL / PostgreSQL：二选一，取消对应示例块的注释并填入真实连
# 接信息。DSN 中的密码等敏感信息建议通过 ${ENV_VAR} 环境变量引用。
# 注意：同一时间只能启用一个 database 块，否则 yaml 解析会冲突。

# ─── 示例 1：MySQL ───
#database:
#  driver: mysql
#  dsn: "user:${GROOT_DB_PASSWORD}@tcp(host:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC"
#  max_open_conns: 20                   # 最大打开连接数（默认 20）
#  max_idle_conns: 5                    # 最大空闲连接数（默认 5）
#  conn_max_lifetime: 30m               # 连接最大生命周期（默认 30m）

# ─── 示例 2：PostgreSQL ───
#database:
#  driver: postgres
#  dsn: "host=host port=5432 user=groot password=${GROOT_DB_PASSWORD} dbname=groot sslmode=disable TimeZone=UTC"
#  max_open_conns: 20
#  max_idle_conns: 5
#  conn_max_lifetime: 30m
`
}
