package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/zfd81/groot/internal/repo"
)

const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testKey() *repo.APIKey {
	created := time.Date(2026, 9, 2, 12, 0, 0, 0, time.Local)
	return &repo.APIKey{
		ID:          "20260902120000",
		Name:        "svc-a",
		Permissions: []string{"chat", "status"},
		ExpiresAt:   created.AddDate(0, 0, 7),
		CreatedAt:   created,
	}
}

// TestSign_Deterministic 同元数据 + 同 secret 多次签发输出字节级一致（还原恒等性的基础）。
func TestSign_Deterministic(t *testing.T) {
	k := testKey()
	t1, err := Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	t2, err := Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign again: %v", err)
	}
	if t1 != t2 {
		t.Errorf("Sign not deterministic:\n%s\n%s", t1, t2)
	}
	if len(strings.Split(t1, ".")) != 3 {
		t.Errorf("not a JWT: %s", t1)
	}
}

// TestVerify_RoundTrip 签发后验证返回原 jti。
func TestVerify_RoundTrip(t *testing.T) {
	token, err := Sign(testKey(), testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	jti, err := Verify(token, testSecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if jti != "20260902120000" {
		t.Errorf("jti = %q, want 20260902120000", jti)
	}
}

// TestVerify_Expired 过期 token 拒绝。
func TestVerify_Expired(t *testing.T) {
	k := testKey()
	k.CreatedAt = time.Now().AddDate(0, 0, -8)
	k.ExpiresAt = time.Now().AddDate(0, 0, -1)
	token, err := Sign(k, testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify(token, testSecret); err == nil {
		t.Error("expired token should fail")
	}
}

// TestVerify_WrongSecret 错误 secret 验签失败（换 secret 即全部失效）。
func TestVerify_WrongSecret(t *testing.T) {
	token, err := Sign(testKey(), testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify(token, "another-secret"); err == nil {
		t.Error("wrong secret should fail")
	}
}

// TestVerify_Tampered 篡改载荷验签失败。
func TestVerify_Tampered(t *testing.T) {
	token, err := Sign(testKey(), testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(token, ".")
	tampered := parts[0] + ".eyJqdGkiOiJoYWNrZWQifQ." + parts[2]
	if _, err := Verify(tampered, testSecret); err == nil {
		t.Error("tampered token should fail")
	}
}

// TestVerify_Garbage 非 JWT 字符串（如旧版随机串 Key）拒绝。
func TestVerify_Garbage(t *testing.T) {
	for _, s := range []string{"", "not-a-jwt", "a.b", "a.b.c.d"} {
		if _, err := Verify(s, testSecret); err == nil {
			t.Errorf("garbage %q should fail", s)
		}
	}
}

// TestVerify_AlgNone alg:none token 拒绝（算法白名单回归测试）。
func TestVerify_AlgNone(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"jti": "x",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign alg:none: %v", err)
	}
	if _, err := Verify(token, testSecret); err == nil {
		t.Error("alg:none token should fail")
	}
}

// TestSignVerify_EmptySecret 空 secret 签发与验证均拒绝（纵深防御）。
func TestSignVerify_EmptySecret(t *testing.T) {
	if _, err := Sign(testKey(), ""); err == nil {
		t.Error("Sign with empty secret should fail")
	}
	token, err := Sign(testKey(), testSecret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify(token, ""); err == nil {
		t.Error("Verify with empty secret should fail")
	}
}
