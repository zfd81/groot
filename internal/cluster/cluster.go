package cluster

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

const (
	heartbeatInterval = 3 * time.Second
	heartbeatTimeout  = 7 * time.Second
)

// Cluster manages instance registration, heartbeat, and leader election
// using a MemberRepo for persistence.
type Cluster struct {
	host  string
	port  int
	regID string
	role  string
	mu    sync.RWMutex
	log   *logger.Logger
	repo  repo.MemberRepo

	onBecomeLeader func()
	onLoseLeader   func()

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Cluster instance backed by memberRepo.
func New(host string, port int, log *logger.Logger, memberRepo repo.MemberRepo) *Cluster {
	return &Cluster{host: host, port: port, log: log, repo: memberRepo}
}

// SetCallbacks sets the callbacks for leader role changes.
func (c *Cluster) SetCallbacks(onBecomeLeader, onLoseLeader func()) {
	c.onBecomeLeader = onBecomeLeader
	c.onLoseLeader = onLoseLeader
}

// Join registers this instance in the cluster and starts the heartbeat loop.
func (c *Cluster) Join(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.register()
	go c.run()
	return nil
}

// Leave gracefully removes this instance from the cluster.
func (c *Cluster) Leave() {
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.regID == "" {
		return
	}
	if err := c.repo.Remove(context.Background(), c.regID); err != nil {
		c.log.Error("删除注册记录失败", zap.Error(err))
	}
	c.regID = ""
}

// IsLeader returns true if this instance is currently the leader.
func (c *Cluster) IsLeader() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.role == RoleLeader
}

// RegID returns the current registration ID.
func (c *Cluster) RegID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.regID
}

// Role returns the current role.
func (c *Cluster) Role() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.role
}

func (c *Cluster) run() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.heartbeat()
		}
	}
}

func (c *Cluster) heartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.repo.Get(c.ctx, c.regID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			if c.role == RoleLeader && c.onLoseLeader != nil {
				c.onLoseLeader()
			}
			c.register()
			return
		}
		c.log.Warn("自检失败,跳过本轮心跳", zap.Error(err))
		return
	}

	if c.role == RoleLeader {
		c.leaderHeartbeat()
	} else {
		c.followerHeartbeat()
	}
}

func (c *Cluster) register() {
	members, err := c.repo.ListAll(c.ctx)
	if err != nil {
		c.log.Error("列出成员失败", zap.Error(err))
		c.role = RoleFollower
		return
	}
	c.regID = GenerateRegID()
	c.role = DetermineRole(c.regID, members, heartbeatTimeout)
	pid := os.Getpid()
	m := &repo.Member{
		RegID: c.regID, Role: c.role,
		Host: c.host, Port: c.port, Pid: pid,
		HeartbeatAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := c.repo.Register(c.ctx, m); err != nil {
		c.log.Error("写入注册记录失败", zap.Error(err))
		return
	}
	c.log.Info("集群注册完成",
		zap.String("reg_id", c.regID),
		zap.String("role", c.role),
		zap.Int("pid", pid),
	)
	if c.role == RoleLeader && c.onBecomeLeader != nil {
		c.onBecomeLeader()
	}
}

func (c *Cluster) leaderHeartbeat() {
	if err := c.repo.Heartbeat(c.ctx, c.regID); err != nil {
		c.log.Error("心跳写入失败", zap.Error(err))
		return
	}
	if err := c.repo.UpdateRole(c.ctx, c.regID, RoleLeader); err != nil {
		c.log.Error("角色更新失败", zap.Error(err))
	}
	n, err := c.repo.RemoveExpired(c.ctx, time.Now().Add(-heartbeatTimeout))
	if err != nil {
		c.log.Warn("清理超时成员失败", zap.Error(err))
	} else if n > 0 {
		c.log.Info("清理超时成员", zap.Int("count", n))
	}
}

func (c *Cluster) followerHeartbeat() {
	members, err := c.repo.ListAll(c.ctx)
	if err != nil {
		c.log.Warn("列出成员失败,跳过本轮心跳", zap.Error(err))
		return
	}
	now := time.Now()
	var alive []*repo.Member
	for _, m := range members {
		if now.Sub(m.HeartbeatAt) < heartbeatTimeout {
			alive = append(alive, m)
		}
	}
	sort.Slice(alive, func(i, j int) bool { return alive[i].RegID < alive[j].RegID })
	if len(alive) > 0 && alive[0].RegID == c.regID {
		c.role = RoleLeader
		c.repo.UpdateRole(c.ctx, c.regID, RoleLeader)
		c.log.Info("提升为 leader", zap.String("reg_id", c.regID))
		c.repo.RemoveExpired(c.ctx, time.Now().Add(-heartbeatTimeout))
		if c.onBecomeLeader != nil {
			c.onBecomeLeader()
		}
	} else {
		c.repo.Heartbeat(c.ctx, c.regID)
	}
}

// GenerateRegID generates a 17-digit millisecond timestamp reg ID.
func GenerateRegID() string {
	s := time.Now().Format("20060102150405.000")
	return strings.Replace(s, ".", "", 1)
}
