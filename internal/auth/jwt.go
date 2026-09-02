// Package auth 提供 API Key（JWT）的签发与验证。
// 签发是确定性的：同一份元数据 + 同一 secret，任何时候输出的 JWT 字节级一致，
// 因此完整 Key 无需落库，可随时由数据库元数据还原（jwt.MapClaims 底层是 map，
// json.Marshal 对 map 按 key 排序，保证序列化稳定）。
package auth

import (
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"

	"github.com/zfd81/groot/internal/repo"
)

// ErrInvalidToken 验签失败、格式非法或已过期。
// 对外统一映射为 401 且不区分原因，避免向攻击者泄露信息。
var ErrInvalidToken = errors.New("auth: invalid token")

// Sign 用 secret 对 API Key 元数据签发 HS256 JWT。
func Sign(k *repo.APIKey, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("auth sign: secret 不能为空")
	}
	perms := k.Permissions
	if perms == nil {
		perms = []string{}
	}
	claims := jwt.MapClaims{
		"jti":   k.ID,
		"sub":   k.Name,
		"scope": perms,
		"iat":   k.CreatedAt.Unix(),
		"exp":   k.ExpiresAt.Unix(),
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("auth sign: %w", err)
	}
	return s, nil
}

// Verify 验签并校验过期（jwt.Parse 对存在的 exp 自动校验），
// 成功返回 jti（API Key 的数据库 ID），供调用方反查吊销状态。
func Verify(tokenStr, secret string) (string, error) {
	// 空 secret 直接拒绝：纵深防御（配置层已有 EnsureAuthSecret 兜底）。
	if secret == "" {
		return "", ErrInvalidToken
	}
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return "", ErrInvalidToken
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", ErrInvalidToken
	}
	jti, ok := claims["jti"].(string)
	if !ok || jti == "" {
		return "", ErrInvalidToken
	}
	return jti, nil
}
