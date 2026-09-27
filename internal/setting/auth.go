// internal/setting/auth.go
// 认证配置：JWT 签名密钥与 API Key 请求头名。
//
// 密钥存放在配置表而非 bootstrap.yaml，因此集群各节点共享同一密钥，
// 任一节点签发的 API Key 在其余节点均可验证。
//
// 读取时机是启动一次：认证中间件在每个请求上执行，不为它增加数据库查询；
// 更换密钥会使全部已签发的 API Key 立即失效，属于重启级别的操作。
package setting

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// 认证配置在配置表中的键名。
const (
	KeyAuthSecret     = "security.auth.secret"
	KeyAuthHeaderName = "security.auth.header_name"
)

// Auth 读取认证配置。密钥缺失时 Secret 为空串，
// 由 EnsureAuthSecret 在启动时补齐。
func (s *Settings) Auth(ctx context.Context) (config.AuthConfig, error) {
	out := config.AuthConfig{HeaderName: defaultAuthHeaderName()}
	if s.repo == nil {
		return out, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.AuthConfig{}, err
	}
	if raw, ok := vals[KeyAuthHeaderName]; ok && raw != "" {
		out.HeaderName = raw
	}
	out.Secret = vals[KeyAuthSecret]
	return out, nil
}

// EnsureAuthSecret 返回 JWT 签名密钥：表中已有则原样返回，
// 缺失则生成一个并写入表中。
//
// 幂等是硬要求 —— 每次启动都换一个密钥会让已发出去的 API Key 全部失效。
func (s *Settings) EnsureAuthSecret(ctx context.Context) (string, error) {
	if s.repo == nil {
		return "", ErrNoSettingStore
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return "", err
	}
	if secret := vals[KeyAuthSecret]; secret != "" {
		return secret, nil
	}
	secret, err := config.GenerateAuthSecret()
	if err != nil {
		return "", err
	}
	if err := s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyAuthSecret, Value: secret}); err != nil {
		return "", err
	}
	return secret, nil
}

// RegenerateAuthSecret 生成新的 JWT 签名密钥并写入配置表，返回新密钥。
// 与 EnsureAuthSecret 不同，它无条件替换——调用方（界面上的「重新生成」）
// 已经确认过后果：所有已签发的 API Key 立即失效，且需重启服务才生效。
func (s *Settings) RegenerateAuthSecret(ctx context.Context) (string, error) {
	if s.repo == nil {
		return "", ErrNoSettingStore
	}
	secret, err := config.GenerateAuthSecret()
	if err != nil {
		return "", err
	}
	if err := s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyAuthSecret, Value: secret}); err != nil {
		return "", err
	}
	return secret, nil
}

// authHeaderNamePattern 请求头名的合法形式：字母、数字与连字符，1 到 64 位。
// HTTP 头名的合法字符集更宽，这里收紧到常见形式，避免存入难以排查的怪名字。
var authHeaderNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// SetAuthHeaderName 保存 API Key 请求头名，改动需重启服务才生效。
// 空串（去首尾空白后）表示恢复默认 X-API-Key，删除表行实现；
// 非空值须匹配 ^[A-Za-z0-9-]{1,64}$，不合法返回包装 ErrInvalidSetting 的错误。
func (s *Settings) SetAuthHeaderName(ctx context.Context, name string) error {
	if s.repo == nil {
		return ErrNoSettingStore
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return s.repo.Delete(ctx, repo.ScopeGlobal, "", KeyAuthHeaderName)
	}
	if !authHeaderNamePattern.MatchString(name) {
		return fmt.Errorf("%w: header_name 只允许字母、数字与连字符，长度 1-64", ErrInvalidSetting)
	}
	return s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyAuthHeaderName, Value: name})
}
