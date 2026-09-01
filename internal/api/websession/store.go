// Package websession 提供 Web 界面登录会话的内存存储：
// 随机令牌关联用户 + 滑动续期过期，以及按客户端 IP 的登录失败限速。
// 服务重启后全部会话失效（需重新登录）。
package websession

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// CookieName Web 登录会话 Cookie 名称（handler 与 middleware 共用）
const CookieName = "groot_web_session"

const (
	maxFailures   = 5                // 单个来源在窗口内允许的最大失败次数
	failureWindow = 10 * time.Minute // 失败计数滑动窗口
	globalMax     = 30               // 全局兜底：窗口内所有来源合计的最大失败次数
)

// session 单个会话记录
type session struct {
	userID string
	exp    time.Time
}

// Store 登录会话存储
type Store struct {
	mu          sync.Mutex
	ttl         time.Duration
	sessions    map[string]session     // token -> 会话
	failures    map[string][]time.Time // 来源键 -> 窗口内失败时间点
	globalFails []time.Time            // 不分来源的全局失败时间点（兜底）
	now         func() time.Time       // 可注入时钟，便于测试
}

// NewStore 创建存储，ttl 为登录会话有效期（活跃访问会滑动续期）
func NewStore(ttl time.Duration) *Store {
	return &Store{
		ttl:      ttl,
		sessions: make(map[string]session),
		failures: make(map[string][]time.Time),
		now:      time.Now,
	}
}

// TTL 返回会话有效期
func (s *Store) TTL() time.Duration { return s.ttl }

// Create 为指定用户生成并登记一个新令牌（64 位 hex）
func (s *Store) Create(userID string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极罕见；宁可 panic 也不下发可预测的全零令牌
		panic("websession: 生成随机令牌失败: " + err.Error())
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepExpiredLocked()
	s.sessions[token] = session{userID: userID, exp: s.now().Add(s.ttl)}
	return token
}

// sweepExpiredLocked 惰性清除所有已过期会话（调用方须持锁）。
// 在 Create 时顺带执行，避免过期令牌永久驻留 map 造成内存泄漏。
func (s *Store) sweepExpiredLocked() {
	now := s.now()
	for token, sess := range s.sessions {
		if now.After(sess.exp) {
			delete(s.sessions, token)
		}
	}
}

// Validate 校验令牌有效性并返回所属用户；命中即滑动续期，过期令牌顺带清除
func (s *Store) Validate(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return "", false
	}
	if s.now().After(sess.exp) {
		delete(s.sessions, token)
		return "", false
	}
	sess.exp = s.now().Add(s.ttl)
	s.sessions[token] = sess
	return sess.userID, true
}

// Delete 注销令牌
func (s *Store) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// DeleteOtherByUser 删除该用户除 keepToken 外的全部会话（修改密码后踢出其他会话），
// 返回删除数量
func (s *Store) DeleteOtherByUser(userID, keepToken string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for token, sess := range s.sessions {
		if sess.userID == userID && token != keepToken {
			delete(s.sessions, token)
			n++
		}
	}
	return n
}

// RecordFailure 记录一次该来源的登录失败（同时累计全局兜底计数）
func (s *Store) RecordFailure(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.failures[key] = append(s.pruneLocked(key), now)
	s.globalFails = append(s.pruneGlobalLocked(), now)
}

// IsLocked 判断该来源是否因失败过多被锁定；全局失败超过兜底阈值时一律锁定，
// 以防攻击者用大量不同来源键绕过单来源限制。
func (s *Store) IsLocked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 全局兜底：即便单来源未超限，合计失败过多也拒绝
	globalRecent := s.pruneGlobalLocked()
	s.globalFails = globalRecent
	if len(globalRecent) >= globalMax {
		return true
	}
	recent := s.pruneLocked(key)
	// 无有效失败记录时删除 key，避免被伪造来源撑爆 map
	if len(recent) == 0 {
		delete(s.failures, key)
		return false
	}
	s.failures[key] = recent
	return len(recent) >= maxFailures
}

// ClearFailures 登录成功后清零该来源失败计数（不清全局兜底计数）
func (s *Store) ClearFailures(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, key)
}

// pruneLocked 剔除窗口外的失败记录（调用方须持锁）
func (s *Store) pruneLocked(key string) []time.Time {
	cutoff := s.now().Add(-failureWindow)
	var recent []time.Time
	for _, ts := range s.failures[key] {
		if ts.After(cutoff) {
			recent = append(recent, ts)
		}
	}
	return recent
}

// pruneGlobalLocked 剔除窗口外的全局失败记录（调用方须持锁）
func (s *Store) pruneGlobalLocked() []time.Time {
	cutoff := s.now().Add(-failureWindow)
	var recent []time.Time
	for _, ts := range s.globalFails {
		if ts.After(cutoff) {
			recent = append(recent, ts)
		}
	}
	return recent
}
