package memory

import (
	"crypto/rand"
	"fmt"
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