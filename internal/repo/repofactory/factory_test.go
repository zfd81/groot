package repofactory

import (
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/sync"
)

// TestNewRepos_SQLite_SyncResourceNil 锁定 SQLite 单机模式下 Resource 与
// SyncResource 的语义分离:Resource 指向本地文件系统(Web 端文件面板等在用),
// 而 SyncResource 为 nil —— 本地没有可比较的远端。
func TestNewRepos_SQLite_SyncResourceNil(t *testing.T) {
	homeDir := t.TempDir()
	sqlxDB, dialect, err := db.Open(nil, homeDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlxDB.Close()
	if dialect != db.DialectSQLite {
		t.Fatalf("dialect = %v, want DialectSQLite", dialect)
	}

	repos := NewRepos(sqlxDB, dialect, homeDir)
	if repos.SyncResource != nil {
		t.Errorf("SyncResource = %T, want nil（SQLite 无可比较的远端）", repos.SyncResource)
	}
	if repos.Resource == nil {
		t.Error("Resource 不应为 nil（本地文件系统实现仍要提供）")
	}
}

// TestNewRepos_MySQL_SyncResourceSharesResource 锁定 MySQL 方言下同步的远端
// 就是 Resource 本身(同一个实例)。NewRepos 只构造 repo 实例、不发起查询,
// 因此这里用未连接的 sqlx.DB 即可。
func TestNewRepos_MySQL_SyncResourceSharesResource(t *testing.T) {
	sqlxDB := sqlx.NewDb(nil, "mysql")
	repos := NewRepos(sqlxDB, db.DialectMySQL, t.TempDir())

	if repos.SyncResource == nil {
		t.Fatal("SyncResource 不应为 nil（MySQL 模式启用同步）")
	}
	if repos.SyncResource != repos.Resource {
		t.Errorf("SyncResource(%p) 应与 Resource(%p) 是同一个实例",
			repos.SyncResource, repos.Resource)
	}
}

// TestNewRepos_SQLite_SyncManagerDisabled 回归测试:把 SQLite 下的
// SyncResource 交给 sync.NewSyncManager,Diff 必须返回 ErrSyncDisabled。
// 若误将 Resource(本地文件系统实现)传入,两侧读的是同一个 homeDir,
// Diff 会返回 IsEmpty=true 而非报错 —— 用户会得到「无差异」的假象。
func TestNewRepos_SQLite_SyncManagerDisabled(t *testing.T) {
	homeDir := t.TempDir()
	sqlxDB, dialect, err := db.Open(nil, homeDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlxDB.Close()

	repos := NewRepos(sqlxDB, dialect, homeDir)
	mgr := sync.NewSyncManager(homeDir, repos.SyncResource)

	if _, err := mgr.Diff(nil); err != sync.ErrSyncDisabled {
		t.Errorf("Diff err = %v, want ErrSyncDisabled", err)
	}
	if err := mgr.Push(nil); err != sync.ErrSyncDisabled {
		t.Errorf("Push err = %v, want ErrSyncDisabled", err)
	}
	if err := mgr.Pull(nil); err != sync.ErrSyncDisabled {
		t.Errorf("Pull err = %v, want ErrSyncDisabled", err)
	}
}

func TestNewRepos_MessageRepoWired(t *testing.T) {
	homeDir := t.TempDir()
	sqlxDB, dialect, err := db.Open(nil, homeDir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlxDB.Close()

	repos := NewRepos(sqlxDB, dialect, homeDir)
	if repos.Message == nil {
		t.Fatal("Message repo 不应为 nil")
	}
}
