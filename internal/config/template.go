package config

// GenerateConfigTemplate returns a config template with helpful comments
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

# Skills 热插拔配置
#skills:
#  hot_reload:
#    enabled: true                  # 是否启用热插拔
#    debounce_delay: 2              # 防抖延迟（秒）

# ReAct 执行配置
#react:
#  max_iterations: 20               # 最大循环次数
#  max_tokens: 100000               # 最大 Token 消耗
#  step_timeout: 60                 # 单步执行超时（秒）
#  error_retry: 2                   # 单步失败重试次数
#  nesting_max_depth: 3             # Skills 嵌套最大深度

# 附件处理配置
#attachment:
#  max_size: 50                     # 单个附件最大大小（MB）
#  max_total_size: 100              # 附件总大小上限（MB）
#  max_count: 10                    # 附件数量上限
#  allowed_types: []                # 允许的附件类型（空数组表示允许所有）

# 记忆模块配置
#memory:
#  directory: memory                # 记忆目录
#  retention_days: 7                # 会话保留天数
#  cleanup_schedule: "02:00"        # 清理时间（HH:MM）
#  history_window: 20               # LLM 上下文窗口（轮次），-1 不限制

# 定时任务调度配置
#schedule:
#  enabled: false                  # 是否允许在对话中创建定时任务（默认关闭，不影响系统级清理任务）
#  max_concurrent_tasks: 3         # 最大并发执行数
#  sync_interval: 30s              # 目录同步间隔

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
