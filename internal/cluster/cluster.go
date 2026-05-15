package cluster

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

const (
	heartbeatInterval = 3 * time.Second
	heartbeatTimeout  = 7 * time.Second
)

// Cluster manages instance registration, heartbeat, and leader election.
type Cluster struct {
	homeDir string
	host    string
	port    int
	regID   string
	role    string
	mu      sync.RWMutex
	log     *logger.Logger

	onBecomeLeader func()
	onLoseLeader   func()

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Cluster instance.
func New(homeDir, host string, port int, log *logger.Logger) *Cluster {
	return &Cluster{
		homeDir: homeDir,
		host:    host,
		port:    port,
		log:     log,
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

	membersDir, err := EnsureMembersDir(c.homeDir)
	if err != nil {
		return err
	}

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

	membersDir, _ := EnsureMembersDir(c.homeDir)
	if membersDir != "" {
		if err := RemoveFile(membersDir, c.regID); err != nil {
			c.log.Error("删除注册文件失败", zap.Error(err))
		}
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
	if _, err := os.Stat(ownPath); os.IsNotExist(err) {
		// File lost -- re-register
		if c.role == RoleLeader {
			if c.onLoseLeader != nil {
				c.onLoseLeader()
			}
		}
		c.register(membersDir)
		return
	}

	if c.role == RoleLeader {
		c.leaderHeartbeat(membersDir)
	} else {
		c.followerHeartbeat(membersDir)
	}
}

func (c *Cluster) register(membersDir string) {
	// List existing members first to detect regID collisions
	members, err := ListMembers(membersDir)
	if err != nil {
		c.log.Error("列出成员失败", zap.Error(err))
		c.role = RoleFollower
		return
	}

	// Build a set of existing member IDs for collision detection
	existingIDs := make(map[string]bool, len(members))
	for _, m := range members {
		existingIDs[m.ID] = true
	}

	// Generate a regID that does not collide with any existing member
	c.regID = GenerateRegID()
	for existingIDs[c.regID] {
		time.Sleep(time.Millisecond)
		c.regID = GenerateRegID()
	}

	c.role = DetermineRole(c.regID, members, heartbeatTimeout)

	pid := os.Getpid()
	if err := WriteRegistration(membersDir, c.regID, c.role, c.host, c.port, pid); err != nil {
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
	if err := WriteRegistration(membersDir, c.regID, RoleLeader, c.host, c.port, pid); err != nil {
		c.log.Error("心跳写入失败", zap.Error(err))
		return
	}

	// Clean up stale registration files
	entries, err := os.ReadDir(membersDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() == c.regID {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > heartbeatTimeout {
			if err := RemoveFile(membersDir, entry.Name()); err != nil {
				c.log.Error("清理超时文件失败", zap.String("file", entry.Name()), zap.Error(err))
			} else {
				c.log.Info("清理超时注册文件", zap.String("file", entry.Name()))
			}
		}
	}
}

func (c *Cluster) followerHeartbeat(membersDir string) {
	members, _ := ListMembers(membersDir)

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
		WriteRegistration(membersDir, c.regID, RoleLeader, c.host, c.port, pid)

		c.log.Info("提升为 leader", zap.String("reg_id", c.regID))

		// Clean up stale files
		entries, _ := os.ReadDir(membersDir)
		for _, entry := range entries {
			if entry.Name() == c.regID {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) > heartbeatTimeout {
				RemoveFile(membersDir, entry.Name())
				c.log.Info("清理超时注册文件", zap.String("file", entry.Name()))
			}
		}

		if c.onBecomeLeader != nil {
			c.onBecomeLeader()
		}
	} else {
		pid := os.Getpid()
		WriteRegistration(membersDir, c.regID, RoleFollower, c.host, c.port, pid)
	}
}
