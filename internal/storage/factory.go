package storage

import (
	"fmt"

	"github.com/zfd81/groot/internal/config"
)

// New 根据配置创建合适的 Storage 实现：
// 未配置 minio 时返回 Local；配置 minio 时返回 Minio。
//
// 注：MinioConfig 的 AccessKey 和 SecretKey 在配置加载阶段已通过
// config.expandConfigEnvVars 处理过 ${ENV_VAR} 形式的环境变量展开，
// factory 这里只负责读取已展开的值并校验非空。
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
	if mc.AccessKey == "" {
		return nil, fmt.Errorf("storage: minio.access_key is required (set directly or via ${ENV_VAR})")
	}
	if mc.SecretKey == "" {
		return nil, fmt.Errorf("storage: minio.secret_key is required (set directly or via ${ENV_VAR})")
	}
	return NewMinio(mc.Endpoint, mc.AccessKey, mc.SecretKey, mc.Bucket, mc.UseSSL)
}
