package memory

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// GenerateSessionID 生成会话ID
// 格式: {YYYYMMDDHHMMSSmmm}_{random4}
func GenerateSessionID() string {
	now := time.Now()
	ts := now.Format("20060102150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	random := randomString(4)
	return fmt.Sprintf("%s_%s", ts, random)
}

// GenerateChatID 生成对话ID
// 格式: chat_{YYYYMMDDHHMMSSmmm}
func GenerateChatID() string {
	now := time.Now()
	ts := now.Format("20060102150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	return fmt.Sprintf("chat_%s", ts)
}

// GenerateStepID 生成步骤ID
// 格式: {YYYYMMDD}-{HHMMSSmmm}-{random6}
func GenerateStepID() string {
	now := time.Now()
	date := now.Format("20060102")
	timeStr := now.Format("150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	random := randomString(6)
	return fmt.Sprintf("%s-%s-%s", date, timeStr, random)
}

// randomString 生成指定长度的随机字符串（小写字母+数字）
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	randBytes := make([]byte, n)
	_, _ = rand.Read(randBytes)
	for i := range b {
		b[i] = letters[int(randBytes[i])%len(letters)]
	}
	return string(b)
}

// GenerateChildChatID 生成子 Agent 的 chatID。
// 格式: {parentChatID}_{HHMMSSmmm}_{random4}_{agentName}
// parentChatID 已含完整日期；子时间戳只保留 HHMMSSmmm；random4 避免同毫秒并发冲突。
//
// 实现说明：纯随机 4 字符在同毫秒高频生成下存在生日悖论碰撞风险（36^4≈1.68M），
// 因此采用「每毫秒随机起点 + 同毫秒内严格自增」策略：random4 = base36((offset+counter) mod 36^4, 4)。
// 同一毫秒内 counter 严格递增（远小于 36^4），保证不重复；跨毫秒由 HHMMSSmmm 区分，整体 ID 仍唯一。
const childIDBase36Range = 36 * 36 * 36 * 36 // 36^4

var (
	childIDMu       sync.Mutex
	childIDLastMs   int64
	childIDCounter  uint32
	childIDOffset   uint32
)

func GenerateChildChatID(parentChatID, agentName string) string {
	childIDMu.Lock()
	// 关键：在锁内取 time.Now()，保证同一锁观察到的 ms 单调非递减。
	// 历史 bug：锁外取时，goroutine 调度延迟可能让「老 ms」在锁内后到达，
	// 触发 ms != lastMs 分支重置 counter+重抽 offset，生日悖论下与历史 offset 碰撞 → 复现历史 ID。
	now := time.Now()
	ms := now.UnixNano() / int64(time.Millisecond)

	// 仅当 ms 严格大于 lastMs 时才重置 counter/offset。
	// - 等于 lastMs：同一毫秒窗口，沿用 counter 自增即可保证唯一。
	// - 小于 lastMs：极罕见的系统时钟回退（NTP 校时等）。此时把 ms 提升到 lastMs，
	//   并同步重算 timeStr，防止「老 timeStr + 新 counter」与历史窗口的 ID 碰撞。
	if ms > childIDLastMs {
		childIDLastMs = ms
		childIDCounter = 0
		var seedBytes [4]byte
		_, _ = rand.Read(seedBytes[:])
		seed := uint32(seedBytes[0])<<24 | uint32(seedBytes[1])<<16 | uint32(seedBytes[2])<<8 | uint32(seedBytes[3])
		childIDOffset = seed % childIDBase36Range
	} else if ms < childIDLastMs {
		ms = childIDLastMs
		now = time.Unix(0, ms*int64(time.Millisecond))
	}
	timeStr := now.Format("150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	v := (childIDOffset + childIDCounter) % childIDBase36Range
	childIDCounter++
	childIDMu.Unlock()

	random := base36Encode4(v)
	return fmt.Sprintf("%s_%s_%s_%s", parentChatID, timeStr, random, agentName)
}

// base36Encode4 把 0..36^4-1 编码为定长 4 位 base36 字符串（小写字母+数字）。
func base36Encode4(v uint32) string {
	const letters = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 4)
	for i := 3; i >= 0; i-- {
		b[i] = letters[v%36]
		v /= 36
	}
	return string(b)
}
