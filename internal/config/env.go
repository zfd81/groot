package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// EnvFileName 是基础设施环境配置文件的固定文件名（与 config.yaml 同目录）。
const EnvFileName = "env.yaml"

// envFile 描述 ~/.groot/env.yaml 的顶层结构。当前仅承载 storage.minio
// 一节；未来如有更多基础设施凭据（如 redis、kafka），按相同方式扩展。
type envFile struct {
	Minio *MinioConfig `yaml:"minio"`
}

// loadEnvFile 读取 homeDir 下的 env.yaml 并把其中的 minio 节注入 cfg。
//
// 行为约定（对应设计 1.6 / 1.7 节）：
//   - 入口必先把 cfg.Storage.Minio 置 nil，确保 config.yaml 里残留的
//     storage.minio 节不再生效（解耦基础设施凭据与业务配置）
//   - env.yaml 不存在 → 保持 nil（local 模式）
//   - env.yaml 存在但 minio 节缺失/为空 → 保持 nil（local 模式）
//   - env.yaml 存在且 minio 节有效 → 赋值给 cfg.Storage.Minio
//
// 环境变量展开（如 ${MINIO_ACCESS_KEY}）由调用方后续的 expandConfigEnvVars
// 统一处理，本函数只负责"按 yaml 原样注入"。
func loadEnvFile(cfg *Config, homeDir string) error {
	cfg.Storage.Minio = nil

	envPath := filepath.Join(homeDir, EnvFileName)
	data, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read env file: %w", err)
	}

	var ef envFile
	if err := yaml.Unmarshal(data, &ef); err != nil {
		return fmt.Errorf("failed to parse env file: %w", err)
	}

	if ef.Minio == nil {
		return nil
	}

	cfg.Storage.Minio = ef.Minio
	return nil
}
