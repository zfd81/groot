package cluster

import (
	"net"
)

// ResolveAdvertiseHost 把配置的监听地址解析为集群成员表中登记的对外地址。
//
// server.host 是监听语义（bind address）：0.0.0.0、:: 与空串表示"监听所有网卡"，
// 它们不是任何一块网卡的真实 IP，直接登记会让多实例集群的成员列表无法区分与定位。
// 规则：
//   - 配置为具体地址（IP 或主机名）时尊重配置，原样返回；
//   - 配置为不可路由的通配地址（0.0.0.0 / :: / 空串）时探测本机对外 IP。
//
// 探测策略：先用 UDP "连接" 一个公网地址让内核按默认路由选源地址
// （UDP 无握手，不会真正发包，离线环境同样适用——只要有默认路由）；
// 失败则回退遍历网卡取第一个非环回 IPv4；再失败返回 127.0.0.1 兜底，
// 保证单机场景仍可用。
func ResolveAdvertiseHost(bindHost string) string {
	if !isWildcardHost(bindHost) {
		return bindHost
	}
	if ip := probeOutboundIP(); ip != "" {
		return ip
	}
	if ip := firstNonLoopbackIPv4(); ip != "" {
		return ip
	}
	return "127.0.0.1"
}

// isWildcardHost 判断是否为"监听所有网卡"的通配地址。
func isWildcardHost(host string) bool {
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// probeOutboundIP 通过 UDP 拨号让内核选默认路由的源地址。
// 目标地址只参与路由决策，UDP Dial 不产生任何网络流量。
func probeOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP.IsUnspecified() {
		return ""
	}
	return addr.IP.String()
}

// firstNonLoopbackIPv4 遍历网卡取第一个已启用网卡上的非环回 IPv4 地址。
func firstNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}
