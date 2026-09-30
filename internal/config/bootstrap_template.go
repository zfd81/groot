package config

// GenerateBootstrapTemplate 返回 ~/.groot/bootstrap.yaml 的初始模板。
//
// 模板内容全注释：每一项都由代码提供缺省值，使用者只在需要偏离缺省值时
// 取消对应行的注释。业务配置不出现在模板中 —— 它们存放于数据库配置表，
// 由 Web 设置面板维护。
func GenerateBootstrapTemplate() string {
	return `# Groot 配置文件
#
# 本文件承载启动时读取的配置：服务监听、日志、数据库连接，以及消息层与
# 调度器的构造参数。其余业务配置（模型、限流阈值、通知渠道、附件限制、
# 定时任务开关等）存放在数据库中，请启动服务后在 Web 设置面板中维护。
#
# 全部配置项都有缺省值，整个文件保持注释即可正常启动。
# 改动本文件需重启服务才生效。

# ─── Agent 元信息 ───
#agent:
#  name: groot                        # Agent 名称
#  version: 1.0.0                     # Agent 版本号

# ─── HTTP 服务 ───
#server:
#  host: 0.0.0.0                      # 监听地址
#  port: 8080                         # 监听端口

# ─── 日志 ───
#logging:
#  level: info                        # 日志级别：debug/info/warn/error
#  format: json                       # 日志格式：json/text
#  output: [stdout, file]             # 输出目标
#  file:
#    directory: logs                  # 日志目录（相对路径以 GROOT_HOME 为基准）
#    filename_pattern: groot-{date}.log
#    max_age: 7                       # 日志保留天数

# ─── 调度器构造参数 ───
# 是否允许模型创建定时任务，在设置面板中开关。
#schedule:
#  max_concurrent_tasks: 3            # 最大并发执行数
#  sync_interval: 30s                 # 目录同步间隔

# ─── 限流后台协程 ───
# 限流开关与各项阈值在设置面板中配置。
#security:
#  rate_limit:
#    cleanup_interval: 5m             # 空闲限流器回收周期

# ─── 数据库 ───
# 整节保持注释即为 SQLite 本地模式（数据库文件 GROOT_HOME/groot.db），零配置。
# 启用 MySQL / PostgreSQL：二选一，取消对应示例块的注释并填入真实连接信息。
# DSN 中的密码建议通过 ${ENV_VAR} 引用环境变量。
# 注意：同一时间只能启用一个 database 块，否则 yaml 解析会冲突。

# ─── 示例 1：MySQL ───
#database:
#  driver: mysql
#  dsn: "user:${GROOT_DB_PASSWORD}@tcp(host:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC"
#  max_open_conns: 20                 # 最大打开连接数（默认 20）
#  max_idle_conns: 5                  # 最大空闲连接数（默认 5）
#  conn_max_lifetime: 30m             # 连接最大生命周期（默认 30m）

# ─── 示例 2：PostgreSQL ───
#database:
#  driver: postgres
#  dsn: "host=host port=5432 user=groot password=${GROOT_DB_PASSWORD} dbname=groot sslmode=disable TimeZone=UTC"
#  max_open_conns: 20
#  max_idle_conns: 5
#  conn_max_lifetime: 30m

# 完整配置说明请参考：https://github.com/zfd81/groot
`
}
