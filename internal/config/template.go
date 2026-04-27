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
      max_tokens: 4096                       # 最大 Token 数
      temperature: 0.7                       # 温度参数

# 其他配置项均有默认值，可按需修改
# 完整配置说明请参考：https://github.com/zfd81/groot
`
}