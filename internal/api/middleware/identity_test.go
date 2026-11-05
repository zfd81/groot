package middleware

import (
	"strings"
	"testing"
)

// TestUserIDHeader 声明终端用户的请求头名称为 OpenAI 风格的 "user"（小写、无前缀）。
func TestUserIDHeader(t *testing.T) {
	if UserIDHeader != "user" {
		t.Fatalf("got %q", UserIDHeader)
	}
}

// TestValidIdentifier 标识校验：trim 后长度 1..64 且仅含字母、数字、下划线、连字符。
func TestValidIdentifier(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"alice", "alice", true}, {" alice ", "alice", true}, {"a_b-9", "a_b-9", true},
		{"A1", "A1", true}, {"中文", "", false}, {"a.b", "", false},
		{"", "", false}, {"   ", "", false}, {"alice bob", "", false}, {"a/b", "", false},
		{strings.Repeat("x", 64), strings.Repeat("x", 64), true}, {strings.Repeat("x", 65), "", false},
	}
	for _, c := range cases {
		got, ok := ValidIdentifier(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ValidIdentifier(%q)=(%q,%v) want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
