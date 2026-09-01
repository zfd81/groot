package db

import (
	"testing"
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
