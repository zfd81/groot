package config

// GenerateEnvTemplate 返回 ~/.groot/env.yaml 的初始模板。
//
// 设计要求（1.6 节）：内容**全注释**，用户取消注释并填值即可启用 MinIO；
// 如保持注释或删除整个文件，则使用本地磁盘存储（零配置）。
//
// 注意：minio 子节使用"先缩进后 #"格式（如 "  #endpoint:"），用户删掉行首
// # 后 yaml 缩进自动正确。新增字段请遵循同样格式。
func GenerateEnvTemplate() string {
	return `# Groot 基础设施环境配置
# 存放 MinIO 等外部服务的连接凭据，与业务配置 (config.yaml) 解耦。
#
# 默认情况下整个文件为注释，附件等文件存储走本地磁盘（零配置）。
# 如需启用 MinIO 对象存储，取消下方 minio 块的注释并填入连接信息：
#   - 删除整个文件 → 回退到本地磁盘存储
#   - 删除 minio 节（或保持注释）→ 回退到本地磁盘存储
#   - 完整填写 minio 节 → 启用 MinIO

#minio:
#  endpoint: localhost:9000          # MinIO 服务地址（host:port）
#  access_key: ${MINIO_ACCESS_KEY}   # 访问密钥（建议使用环境变量）
#  secret_key: ${MINIO_SECRET_KEY}   # 密钥（建议使用环境变量）
#  bucket: groot                     # 存储桶名称
#  use_ssl: false                    # 是否启用 HTTPS
`
}
