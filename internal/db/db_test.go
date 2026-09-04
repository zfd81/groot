package db

import (
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/config"
)

func TestOpen_SQLite(t *testing.T) {
	db, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if dialect != DialectSQLite {
		t.Errorf("expected SQLite dialect, got %v", dialect)
	}
	tables := []string{
		"cluster_members", "schedule_tasks", "schedule_executions",
		"memory_sessions", "memory_chats", "shared_resources", "users",
	}
	for _, tbl := range tables {
		var n int
		err := db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n)
		if err != nil {
			t.Errorf("table %s not accessible: %v", tbl, err)
		}
	}
}

// TestResolveDriver_SQLiteIsPureGo 锁定 SQLite 走纯 Go 驱动（driver 名 "sqlite"，
// 由 modernc.org/sqlite 注册）。若误改回 cgo 版的 "sqlite3"，交叉编译产物会在
// 运行时拿到 go-sqlite3 的 !cgo 桩，Open 时才报错——这里在编译期就钉住。
func TestResolveDriver_SQLiteIsPureGo(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.DatabaseConfig
	}{
		{"nil 配置", nil},
		{"空 driver", &config.DatabaseConfig{}},
		{"显式 sqlite", &config.DatabaseConfig{Driver: "sqlite"}},
		{"未知 driver 回落", &config.DatabaseConfig{Driver: "oracle"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			driver, _, dialect := resolveDriver(c.cfg, t.TempDir())
			if driver != "sqlite" {
				t.Errorf("driver = %q, want \"sqlite\"（纯 Go，不需要 cgo）", driver)
			}
			if dialect != DialectSQLite {
				t.Errorf("dialect = %v, want DialectSQLite", dialect)
			}
		})
	}
}

// TestResolveDriver_SQLitePragmaSyntax 锁定 DSN 用 modernc 的 _pragma=name(value)
// 语法。mattn 风格的 _journal_mode=WAL 在 modernc 下会被当作未知参数报错。
func TestResolveDriver_SQLitePragmaSyntax(t *testing.T) {
	_, dsn, _ := resolveDriver(nil, t.TempDir())
	for _, want := range []string{"_pragma=journal_mode(WAL)", "_pragma=busy_timeout(5000)"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q 缺少 %q", dsn, want)
		}
	}
}

// TestOpen_SQLitePragmasApplied 验证 WAL 与 busy_timeout 真正生效，
// 而不是被驱动静默忽略。
func TestOpen_SQLitePragmasApplied(t *testing.T) {
	sqlxDB, _, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlxDB.Close()

	var journalMode string
	if err := sqlxDB.Get(&journalMode, "PRAGMA journal_mode"); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := sqlxDB.Get(&busyTimeout, "PRAGMA busy_timeout"); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	db, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := Migrate(db, dialect); err != nil {
		t.Errorf("second Migrate: %v", err)
	}
	db.Close()
}

// TestMigrate_DropsLegacyUKSessionRound 模拟一个遗留库带着旧版的
// uk_session_round 唯一索引，调用 Migrate 后应当被清理掉。
func TestMigrate_DropsLegacyUKSessionRound(t *testing.T) {
	db, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// 先把当前 schema 跑一遍（无旧索引），再人工注入老约束模拟 升级路径。
	if _, err := db.Exec(`CREATE UNIQUE INDEX uk_session_round ON memory_chats(session_id, round)`); err != nil {
		t.Fatalf("seed legacy index: %v", err)
	}
	exists, err := indexExists(db, dialect, "memory_chats", "uk_session_round")
	if err != nil || !exists {
		t.Fatalf("legacy index should exist before migrate: exists=%v err=%v", exists, err)
	}

	// 再次跑 Migrate —— 模拟新版本启动时清理老库
	if err := Migrate(db, dialect); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	exists, err = indexExists(db, dialect, "memory_chats", "uk_session_round")
	if err != nil {
		t.Fatalf("indexExists post-migrate: %v", err)
	}
	if exists {
		t.Errorf("uk_session_round should have been dropped by Migrate")
	}

	// 验证我们没误伤新版的非唯一索引
	exists, err = indexExists(db, dialect, "memory_chats", "idx_mc_session_round")
	if err != nil || !exists {
		t.Errorf("idx_mc_session_round should still exist after migrate, exists=%v err=%v", exists, err)
	}
}

// TestMigrate_NoLegacyIndexIsNoop 全新库（没有 uk_session_round）跑 Migrate 不应出错，
// dropLegacyIndices 走 catalog 探测路径返回 false 后跳过 DROP。
func TestMigrate_NoLegacyIndexIsNoop(t *testing.T) {
	db, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	exists, err := indexExists(db, dialect, "memory_chats", "uk_session_round")
	if err != nil {
		t.Fatalf("indexExists: %v", err)
	}
	if exists {
		t.Fatalf("fresh DB should not have legacy uk_session_round")
	}
	if err := Migrate(db, dialect); err != nil {
		t.Errorf("Migrate on fresh DB should be noop, got %v", err)
	}
}
