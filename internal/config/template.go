package config

// GenerateConfigTemplate 返回带注释的 config.yaml 模板。
//
// 注意：数据库等基础设施凭据存放在 ~/.groot/env.yaml，本模板只包含业务配置。
func GenerateConfigTemplate() string {
	return `# Groot Agent 配置文件
# 请根据实际情况修改以下配置

# LLM 配置（必填）
# 请填写你的 LLM API 信息，支持 OpenAI 兼容协议
llm:
  default_model: gpt-4o           # 默认模型名称
  models:
    gpt-4o:
      base_url: https://api.openai.com/v1    # API 地址
      api_key: ${OPENAI_API_KEY}             # API 密钥（建议使用环境变量）
      model: gpt-4o                          # 模型名称
      max_completion_tokens: 4096            # 最大输出 Token 数
      max_context_tokens: 0                  # 输入上下文 Token 预算（0 表示不限制）
      temperature: 0.7                       # 温度参数（0.0~2.0）
      top_p: 1.0                             # 核采样系数（0.0~1.0）
      frequency_penalty: 0.0                 # 频率惩罚（-2.0~2.0）
      presence_penalty: 0.0                  # 存在惩罚（-2.0~2.0）
      seed: 0                                # 随机种子（0 表示不设置）
      stop: []                               # 停止序列
      thinking: false                        # 深度思考模式（Qwen/DeepSeek 等模型）

# ============================================================
# 以下配置项均有默认值，如需修改请取消注释并编辑
# ============================================================

# Agent 基础配置
#agent:
#  name: groot                      # Agent 名称
#  version: 1.0.0                   # Agent 版本号

# HTTP 服务配置
#server:
#  host: 0.0.0.0                    # 服务监听地址
#  port: 8080                       # 服务监听端口

# ReAct 执行配置
#react:
#  max_iterations: 20               # ReAct 循环最大迭代次数
#  step_timeout: 60                 # 单步 LLM 调用超时（秒）
#  error_retry: 2                   # 单步 LLM 调用失败重试次数

# 附件处理配置
#attachment:
#  max_size: 50                     # 单个附件最大大小（MB）
#  max_total_size: 100              # 附件总大小上限（MB）
#  max_count: 10                    # 附件数量上限
#  allowed_types: []                # 允许的附件类型（空数组表示允许所有）

# 记忆模块配置
#memory:
#  history_window: 20               # LLM 上下文窗口（轮次），-1 不限制

# 定时任务调度配置
#schedule:
#  enabled: false                  # 是否允许在对话中创建定时任务（默认关闭）
#  max_concurrent_tasks: 3         # 最大并发执行数
#  sync_interval: 30s              # 目录同步间隔

# 消息通知配置（定时任务执行结果的推送渠道；stdout 始终启用）
#message:
#  queue_size: 256                 # 发送队列容量
#  workers: 2                      # 发送工作协程数
#  senders:
#    webhook:
#      enabled: false              # 是否启用 webhook 通知
#      url: ""                     # Webhook 地址（接收 POST JSON）
#    email:
#      enabled: false              # 是否启用邮件通知
#      smtp_host: ""               # SMTP 服务器地址
#      smtp_port: 587              # SMTP 端口
#      username: ""                # SMTP 用户名
#      password: ""                # SMTP 密码（建议使用环境变量）
#      from: ""                    # 发件人地址

# 子 Agent 调度配置（v3.8）
#subagent:
#  max_concurrency: 5              # 全局并发上限（FIFO 排队）
#  exec_timeout: "5m"              # 子 Agent 执行超时（排队不计入）
#  max_task_length: 16000          # task 参数最大字符数
#  max_result_length: 8000         # 子 Agent 返回文本截断长度

# 安全配置
#security:
#  rate_limit:
#    enabled: false                   # 是否启用速率限制
#    global_qps: 0                    # 全局 QPS 限制（0 表示不限制）
#    global_concurrency: 0            # 全局并发限制（0 表示不限制）
#    default_qps: 10                  # 每个 API Key 的默认 QPS
#    default_concurrency: 5           # 每个 API Key 的默认并发数
#    cleanup_interval: 5m             # 空闲限流器清理间隔
#  auth:
#    enabled: false                 # 是否开启认证
#    type: api_key                  # 认证类型
#    api_key:
#      header_name: X-API-Key       # 认证 Header 名称
#      keys:
#        - name: default            # Key 名称
#          key: ${GROOT_API_KEY}    # Key 值（建议使用环境变量）
#          permissions: [all]       # 权限范围
#  web:
#    enabled: false                 # 是否启用 Web 界面登录认证
#    username: admin                # 登录用户名
#    password: ${GROOT_WEB_PASS}    # 登录密码（建议使用环境变量）
#    session_ttl: 24h               # 登录会话有效期
#    secure: false                  # 会话 Cookie 是否置 Secure（经 https 部署时设为 true）

# 日志配置
#logging:
#  level: info                      # 日志级别：debug/info/warn/error
#  format: json                     # 日志格式：json/text
#  output: [stdout, file]           # 输出目标
#  file:
#    directory: logs                # 日志文件目录
#    filename_pattern: groot-{date}.log  # 文件名模式
#    max_age: 7                     # 日志保留天数

# 完整配置说明请参考：https://github.com/zfd81/groot
`
}
