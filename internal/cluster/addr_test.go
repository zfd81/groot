package cluster

import (
	"net"
	"testing"
)

// TestIsWildcardHost 覆盖通配地址判定：0.0.0.0/::/空串为通配，
// 具体 IP、主机名不是。
func TestIsWildcardHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"", true},
		{"0.0.0.0", true},
		{"::", true},
		{"127.0.0.1", false},
		{"192.168.1.10", false},
		{"fe80::1", false},
		{"groot.internal", false}, // 主机名解析不了 ParseIP，按具体地址对待
	}
	for _, c := range cases {
		if got := isWildcardHost(c.host); got != c.want {
			t.Errorf("isWildcardHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestResolveAdvertiseHost_KeepsExplicitHost 验证配置了具体地址时原样返回，
// 不做任何探测替换。
func TestResolveAdvertiseHost_KeepsExplicitHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "192.168.1.10", "10.0.0.7", "groot.internal"} {
		if got := ResolveAdvertiseHost(host); got != host {
			t.Errorf("ResolveAdvertiseHost(%q) = %q, want unchanged", host, got)
		}
	}
}

// TestResolveAdvertiseHost_ResolvesWildcard 验证通配地址会被解析为一个
// 可解析的具体 IP：结果非空、非通配。真实网络环境各异（可能离线、可能只有
// 环回），只断言不变量：结果是合法 IP 且不再是 0.0.0.0/::。
func TestResolveAdvertiseHost_ResolvesWildcard(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "::"} {
		got := ResolveAdvertiseHost(host)
		ip := net.ParseIP(got)
		if ip == nil {
			t.Errorf("ResolveAdvertiseHost(%q) = %q, not a valid IP", host, got)
			continue
		}
		if ip.IsUnspecified() {
			t.Errorf("ResolveAdvertiseHost(%q) = %q, still a wildcard address", host, got)
		}
	}
}
