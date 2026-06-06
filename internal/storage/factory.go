package storage

import (
	"fmt"

	"github.com/zfd81/groot/internal/config"
)

// New 根据配置创建合适的 Storage 实现：
// 未配置 minio 时返回 Local；配置 minio 时返回 Minio。
//
// MinioConfig 中的 AccessKey 和 SecretKey 支持 ${ENV_VAR} 形式的环境变量展开
// （与 LLMConfig.APIKey 风格一致），便于通过环境变量传入敏感信息。
func New(cfg config.StorageConfig) (Storage, error) {
	if cfg.Minio == nil {
		return NewLocal(), nil
	}
	mc := cfg.Minio
	if mc.Endpoint == "" {
		return nil, fmt.Errorf("storage: minio.endpoint is required")
	}
	if mc.Bucket == "" {
		return nil, fmt.Errorf("storage: minio.bucket is required")
	}

	// 展开环境变量（与 LLMConfig.APIKey 一致的处理方式）
	accessKey := config.ExpandEnv(mc.AccessKey)
	secretKey := config.ExpandEnv(mc.SecretKey)

	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("storage: minio.access_key and secret_key are required")
	}
	return NewMinio(mc.Endpoint, accessKey, secretKey, mc.Bucket, mc.UseSSL)
}
