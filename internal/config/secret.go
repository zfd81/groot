// secret.go 提供 JWT 签名密钥生成与配置文件原子写入的基础能力。
// 密钥的持久化由数据库配置表负责（见 internal/setting 包的兜底逻辑）。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// GenerateAuthSecret 生成 32 字节强随机 JWT 签名密钥（hex 编码，64 字符）。
func GenerateAuthSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机密钥失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// writeFileAtomic 用 tmp+rename 原子写入文件，避免写入中断留下损坏的配置。
// 临时文件直接以 0600 创建（内容可能含敏感凭据）。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
