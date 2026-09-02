package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GenerateAuthSecret 生成 32 字节强随机 JWT 签名密钥（hex 编码，64 字符）。
func GenerateAuthSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机密钥失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// EnsureAuthSecret 确保 cfg 拥有 JWT 签名密钥：已配置则直接返回；
// 为空则生成新密钥、回写 config.yaml 并更新 cfg（覆盖老版本升级场景）。
func EnsureAuthSecret(homeDir string, cfg *Config) error {
	if cfg.Security.Auth.Secret != "" {
		return nil
	}
	secret, err := GenerateAuthSecret()
	if err != nil {
		return err
	}
	configPath := filepath.Join(homeDir, "config.yaml")
	if err := writeSecretToFile(configPath, secret); err != nil {
		return err
	}
	// config.yaml 从此承载 JWT 签名密钥，收紧为仅属主可读写（看齐 env.yaml 的 0600 标准）
	if err := os.Chmod(configPath, 0600); err != nil {
		return fmt.Errorf("设置配置文件权限失败: %w", err)
	}
	cfg.Security.Auth.Secret = secret
	return nil
}

// writeFileAtomic 用 tmp+rename 原子写入文件，避免写入中断留下损坏的配置。
// 临时文件直接以 0600 创建（内容含 JWT 签名密钥）。
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

// writeSecretToFile 把 security.auth.secret 写入配置文件。
// 文件中无活动 security 节（常见：模板全注释）时直接追加文本块，原文一字不动；
// 已有活动 security 节时用 yaml.Node 就地插入（避免重复键），注释随节点保留。
func writeSecretToFile(configPath, secret string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}
	if !hasTopLevelKey(&root, "security") {
		block := fmt.Sprintf("\n# JWT 签名密钥（系统自动生成，请勿泄露；更换后所有 API Key 立即失效）\nsecurity:\n  auth:\n    secret: \"%s\"\n", secret)
		if err := writeFileAtomic(configPath, append(data, []byte(block)...)); err != nil {
			return fmt.Errorf("写入配置文件失败: %w", err)
		}
		return nil
	}
	doc := root.Content[0]
	authNode := ensureMapChild(ensureMapChild(doc, "security"), "auth")
	setMapValue(authNode, "secret", secret)
	// 用 2 空格缩进序列化（yaml.Marshal 默认 4 空格，会重排用户文件的缩进风格）
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := writeFileAtomic(configPath, buf.Bytes()); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}
	return nil
}

// hasTopLevelKey 判断 yaml 文档顶层映射是否存在指定 key。
func hasTopLevelKey(root *yaml.Node, key string) bool {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return false
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == key {
			return true
		}
	}
	return false
}

// ensureMapChild 在映射节点 parent 下取 key 对应的子映射节点；不存在则创建。
func ensureMapChild(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			child := parent.Content[i+1]
			// 键存在但值为 null（子项全被注释）时，就地升级为空映射再使用；
			// 非映射值（含 null 与标量）一律视为无效配置并覆盖为空映射
			if child.Kind != yaml.MappingNode {
				child.Kind = yaml.MappingNode
				child.Tag = "!!map"
				child.Value = ""
				child.Content = nil
			}
			return child
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, k, v)
	return v
}

// setMapValue 设置映射节点下 key 的字符串值；已存在则覆盖。
func setMapValue(parent *yaml.Node, key, value string) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			parent.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
