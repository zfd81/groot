package cluster

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
	istorage "github.com/zfd81/groot/internal/storage"
)

const (
	heartbeatInterval = 3 * time.Second
	heartbeatTimeout  = 7 * time.Second
)

// Cluster manages instance registration, heartbeat, and leader election.
type Cluster struct {
	membersDir string
	host       string
	port       int
	regID      string
	role       string
	mu         sync.RWMutex
	log        *logger.Logger
	store      istorage.Storage

	onBecomeLeader func()
	onLoseLeader   func()

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Cluster instance.
//
// membersDir is the full path (or object-key prefix) of the cluster members
// directory. The caller is responsible for composing the full path
// (e.g. "${homeDir}/cluster/members" in local mode, "cluster/members" in
// minio mode); cluster does not append any sub-path internally.
func New(membersDir, host string, port int, log *logger.Logger, store istorage.Storage) *Cluster {
	return &Cluster{
		membersDir: membersDir,
		host:       host,
		port:       port,
		log:        log,
		store:      store,
	}
}

// SetCallbacks sets the callbacks for leader role changes.
func (c *Cluster) SetCallbacks(onBecomeLeader, onLoseLeader func()) {
	c.onBecomeLeader = onBecomeLeader
	c.onLoseLeader = onLoseLeader
}

// Join registers this instance in the cluster and starts the heartbeat loop.
func (c *Cluster) Join(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	membersDir := c.membersDir

	c.register(membersDir)

	go c.run(membersDir)
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

	membersDir := c.membersDir
	if err := RemoveFile(c.store, membersDir, c.regID); err != nil {
		c.log.Error("删除注册文件失败", zap.Error(err))
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

func (c *Cluster) run(membersDir string) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.heartbeat(membersDir)
		}
	}
}

func (c *Cluster) heartbeat(membersDir string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if own file still exists
	ownPath := filepath.Join(membersDir, c.regID)
	_, err := c.store.Stat(context.Background(), ownPath)
	if err != nil {
		if errors.Is(err, istorage.ErrNotFound) {
			// File lost -- re-register
			if c.role == RoleLeader {
				if c.onLoseLeader != nil {
					c.onLoseLeader()
				}
			}
			c.register(membersDir)
			return
		}
		// 其他错误(权限/IO/网络): 跳过本轮心跳,避免在不确定状态下乐观写入
		c.log.Warn("自检失败,跳过本轮心跳",
			zap.String("path", ownPath),
			zap.Error(err),
		)
		return
	}

	if c.role == RoleLeader {
		c.leaderHeartbeat(membersDir)
	} else {
		c.followerHeartbeat(membersDir)
	}
}

func (c *Cluster) register(membersDir string) {
	// List existing members to determine role
	members, err := ListMembers(c.store, membersDir)
	if err != nil {
		c.log.Error("列出成员失败", zap.Error(err))
		c.role = RoleFollower
		return
	}

	c.regID = GenerateRegID()

	c.role = DetermineRole(c.regID, members, heartbeatTimeout)

	pid := os.Getpid()
	if err := WriteRegistration(c.store, membersDir, c.regID, c.role, c.host, c.port, pid); err != nil {
		c.log.Error("写入注册文件失败", zap.Error(err))
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

func (c *Cluster) leaderHeartbeat(membersDir string) {
	pid := os.Getpid()
	if err := WriteRegistration(c.store, membersDir, c.regID, RoleLeader, c.host, c.port, pid); err != nil {
		c.log.Error("心跳写入失败", zap.Error(err))
		return
	}

	// Clean up stale registration files
	members, err := ListMembers(c.store, membersDir)
	if err != nil {
		c.log.Warn("列出成员失败,跳过本轮 stale 清理", zap.Error(err))
		return
	}
	for _, m := range members {
		if m.ID == c.regID {
			continue
		}
		if time.Since(m.Mtime) > heartbeatTimeout {
			if err := RemoveFile(c.store, membersDir, m.ID); err != nil {
				c.log.Error("清理超时文件失败", zap.String("file", m.ID), zap.Error(err))
			} else {
				c.log.Info("清理超时注册文件", zap.String("file", m.ID))
			}
		}
	}
}

func (c *Cluster) followerHeartbeat(membersDir string) {
	members, err := ListMembers(c.store, membersDir)
	if err != nil {
		c.log.Warn("列出成员失败,跳过本轮心跳", zap.Error(err))
		return
	}

	// Filter alive members
	now := time.Now()
	var alive []MemberInfo
	for _, m := range members {
		if now.Sub(m.Mtime) < heartbeatTimeout {
			alive = append(alive, m)
		}
	}

	// Sort alive members by ID
	sort.Slice(alive, func(i, j int) bool {
		return alive[i].ID < alive[j].ID
	})

	if len(alive) > 0 && c.regID == alive[0].ID {
		// Become leader
		c.role = RoleLeader
		pid := os.Getpid()
		WriteRegistration(c.store, membersDir, c.regID, RoleLeader, c.host, c.port, pid)

		c.log.Info("提升为 leader", zap.String("reg_id", c.regID))

		// Clean up stale files
		for _, m := range members {
			if m.ID == c.regID {
				continue
			}
			if time.Since(m.Mtime) > heartbeatTimeout {
				RemoveFile(c.store, membersDir, m.ID)
				c.log.Info("清理超时注册文件", zap.String("file", m.ID))
			}
		}

		if c.onBecomeLeader != nil {
			c.onBecomeLeader()
		}
	} else {
		pid := os.Getpid()
		WriteRegistration(c.store, membersDir, c.regID, RoleFollower, c.host, c.port, pid)
	}
}
