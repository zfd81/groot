package db

import (
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

// openLegacyDB 直接用 sqlx.Open 建一个空库，而不走同包其它测试用的
// Open(nil, t.TempDir())。原因：Open 内部会先跑一遍 Migrate，表就已经是新
// schema 了，没有机会注入老版本的表结构。测试升级路径必须从裸库开始建老表。
func openLegacyDB(t *testing.T) *sqlx.DB {
	t.Helper()
	sqlxDB, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return sqlxDB
}

// TestMigrate_AddsStatusColumnToLegacySharedResources 模拟老库：shared_resources
// 表由旧版本建出、没有 status 列。Migrate 应当通过 ALTER TABLE 补上该列，
// 且已有数据落到默认值 active。
func TestMigrate_AddsStatusColumnToLegacySharedResources(t *testing.T) {
	sqlxDB := openLegacyDB(t)

	// 模拟旧版本建的表：没有 status 列
	_, err := sqlxDB.Exec(`CREATE TABLE shared_resources (
		path         TEXT NOT NULL PRIMARY KEY,
		content      BLOB NOT NULL,
		content_type TEXT NOT NULL DEFAULT '',
		size         INTEGER NOT NULL,
		content_hash TEXT NOT NULL DEFAULT '',
		updated_at   INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	_, err = sqlxDB.Exec(`INSERT INTO shared_resources (path, content, size, updated_at)
		VALUES ('config.yaml', x'6162', 2, 1757734800000)`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Migrate(sqlxDB, DialectSQLite); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var status string
	if err := sqlxDB.Get(&status, `SELECT status FROM shared_resources WHERE path='config.yaml'`); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if status != "active" {
		t.Fatalf("legacy row status = %q, want \"active\"", status)
	}
}

// TestMigrate_StatusColumnIsIdempotent 加列迁移每次启动都会跑。第二次 Migrate
// 不能把列删了重建、也不能覆盖已有数据，所以这里断言幂等的语义：列仍在、
// 已有行的 status 值不变、行数不变。
func TestMigrate_StatusColumnIsIdempotent(t *testing.T) {
	sqlxDB := openLegacyDB(t)

	if err := Migrate(sqlxDB, DialectSQLite); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	// 种一行非默认值，第二次迁移若重建列就会被打回 active
	_, err := sqlxDB.Exec(`INSERT INTO shared_resources (path, content, size, updated_at, status)
		VALUES ('gone.yaml', x'6162', 2, 1757734800000, 'deleted')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Migrate(sqlxDB, DialectSQLite); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	exists, err := columnExists(sqlxDB, DialectSQLite, "shared_resources", "status")
	if err != nil {
		t.Fatalf("columnExists: %v", err)
	}
	if !exists {
		t.Error("status 列在第二次 Migrate 后消失了")
	}

	var status string
	if err := sqlxDB.Get(&status, `SELECT status FROM shared_resources WHERE path='gone.yaml'`); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if status != "deleted" {
		t.Errorf("seeded row status = %q, want \"deleted\"（第二次迁移不应覆盖已有数据）", status)
	}

	var count int
	if err := sqlxDB.Get(&count, `SELECT COUNT(*) FROM shared_resources`); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("行数 = %d, want 1", count)
	}
}

// TestMigrate_FreshDBHasStatusColumnWithActiveDefault 新库走的是建表 DDL 路径
// （不经过 ALTER），断言建出来就带 status 列，且不指定该列插入时默认为 active。
func TestMigrate_FreshDBHasStatusColumnWithActiveDefault(t *testing.T) {
	sqlxDB, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlxDB.Close()

	exists, err := columnExists(sqlxDB, dialect, "shared_resources", "status")
	if err != nil {
		t.Fatalf("columnExists: %v", err)
	}
	if !exists {
		t.Fatal("全新库的 shared_resources 应当带 status 列")
	}

	_, err = sqlxDB.Exec(`INSERT INTO shared_resources (path, content, size, updated_at)
		VALUES ('fresh.yaml', x'6162', 2, 1757734800000)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	var status string
	if err := sqlxDB.Get(&status, `SELECT status FROM shared_resources WHERE path='fresh.yaml'`); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if status != "active" {
		t.Errorf("默认值 = %q, want \"active\"", status)
	}
}

// TestDDLStatements_SharedResourcesHasStatus 防 DDL 漂移：status 的定义散落在三处
// 建表语句和 statusColumnDef 共四个地方，改一处漏一处不会被别的测试发现。
// 这里对三个方言的建表语句做纯字符串检查，顺带兜住本地跑不到的 MySQL/PG 路径。
func TestDDLStatements_SharedResourcesHasStatus(t *testing.T) {
	for _, d := range []Dialect{DialectSQLite, DialectMySQL, DialectPostgres} {
		var createStmt string
		for _, stmt := range ddlStatements(d) {
			if strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS shared_resources") {
				createStmt = stmt
				break
			}
		}
		if createStmt == "" {
			t.Errorf("dialect %v: 找不到 shared_resources 建表语句", d)
			continue
		}
		if !strings.Contains(createStmt, "status") {
			t.Errorf("dialect %v: 建表语句缺少 status 列:\n%s", d, createStmt)
		}
		if !strings.Contains(createStmt, "DEFAULT 'active'") {
			t.Errorf("dialect %v: status 列缺少 DEFAULT 'active':\n%s", d, createStmt)
		}
	}
}

// TestMigrate_CreatesClusterMessageTables 新库执行 Migrate 后应存在集群消息两张表
// 及其关键列；重复执行不报错（幂等）。
func TestMigrate_CreatesClusterMessageTables(t *testing.T) {
	sqlxDB := openLegacyDB(t)

	if err := Migrate(sqlxDB, DialectSQLite); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(sqlxDB, DialectSQLite); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	checks := []struct{ table, column string }{
		{"cluster_messages", "id"},
		{"cluster_messages", "message_type"},
		{"cluster_messages", "payload"},
		{"cluster_messages", "target_instance"},
		{"cluster_messages", "target_module"},
		{"cluster_messages", "priority"},
		{"cluster_messages", "created_at"},
		{"cluster_messages", "expires_at"},
		{"cluster_messages", "source_instance"},
		{"cluster_message_consumers", "message_id"},
		{"cluster_message_consumers", "instance_id"},
		{"cluster_message_consumers", "consumed_at"},
		{"cluster_message_consumers", "status"},
		{"cluster_message_consumers", "error_message"},
	}
	for _, c := range checks {
		exists, err := columnExists(sqlxDB, DialectSQLite, c.table, c.column)
		if err != nil {
			t.Fatalf("columnExists(%s.%s): %v", c.table, c.column, err)
		}
		if !exists {
			t.Errorf("column %s.%s missing after Migrate", c.table, c.column)
		}
	}

	for _, idx := range []struct{ table, name string }{
		{"cluster_messages", "idx_cm_target_expires"},
		{"cluster_messages", "idx_cm_priority_created"},
		{"cluster_message_consumers", "idx_cmc_instance_consumed"},
	} {
		exists, err := indexExists(sqlxDB, DialectSQLite, idx.table, idx.name)
		if err != nil {
			t.Fatalf("indexExists(%s): %v", idx.name, err)
		}
		if !exists {
			t.Errorf("index %s missing after Migrate", idx.name)
		}
	}
}
