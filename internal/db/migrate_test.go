package db

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
)

// openEmptyDB 直接用 sqlx.Open 建一个空库，不走同包其它测试用的
// Open(nil, t.TempDir())。原因：Open 内部会先跑一遍 Migrate，表就已经建好了；
// 需要从裸库开始观察 Migrate 建表结果的测试，必须自己拿到空库。
func openEmptyDB(t *testing.T) *sqlx.DB {
	t.Helper()
	sqlxDB, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return sqlxDB
}

// columnExists 查询 SQLite 目录判断某表是否有某列。建表策略改为"只新建"后，
// 生产代码不再需要列存在性探测；这里以测试辅助函数的形式保留，用于断言
// Migrate 建出的结构。
func columnExists(sqlxDB *sqlx.DB, table, column string) (bool, error) {
	var n int
	err := sqlxDB.Get(&n, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// indexExists 查询 SQLite 目录判断某表上是否有某索引，用途同 columnExists。
func indexExists(sqlxDB *sqlx.DB, table, indexName string) (bool, error) {
	var n int
	err := sqlxDB.Get(&n,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name=? AND name=?`,
		table, indexName)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// TestMigrate_FreshDBHasStatusColumnWithActiveDefault 新库走建表 DDL 路径，
// 断言建出来就带 status 列，且不指定该列插入时默认为 active。
func TestMigrate_FreshDBHasStatusColumnWithActiveDefault(t *testing.T) {
	sqlxDB, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlxDB.Close()

	if dialect != DialectSQLite {
		t.Fatalf("dialect = %v, want DialectSQLite", dialect)
	}
	exists, err := columnExists(sqlxDB, "shared_resources", "status")
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
// 建表语句里，改一处漏一处不会被别的测试发现。这里对三个方言的建表语句做纯字符
// 串检查，顺带兜住本地跑不到的 MySQL/PG 路径。
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
	sqlxDB := openEmptyDB(t)

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
		exists, err := columnExists(sqlxDB, c.table, c.column)
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
		exists, err := indexExists(sqlxDB, idx.table, idx.name)
		if err != nil {
			t.Fatalf("indexExists(%s): %v", idx.name, err)
		}
		if !exists {
			t.Errorf("index %s missing after Migrate", idx.name)
		}
	}
}

// TestMigrate_SettingsTable 新库执行 Migrate 后 settings 表应可读写，且复合主键生效
func TestMigrate_SettingsTable(t *testing.T) {
	sqlxDB, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlxDB.Close()
	if err := Migrate(sqlxDB, dialect); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ins := fmt.Sprintf(
		"INSERT INTO settings (scope, scope_id, name, value, updated_at) VALUES (%s)",
		dialect.Placeholders(5))
	if _, err := sqlxDB.Exec(ins, "global", "", "voice.model", "whisper-1", int64(1)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got string
	q := fmt.Sprintf(
		"SELECT value FROM settings WHERE scope=%s AND scope_id=%s AND name=%s",
		dialect.Placeholder(1), dialect.Placeholder(2), dialect.Placeholder(3))
	if err := sqlxDB.Get(&got, q, "global", "", "voice.model"); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "whisper-1" {
		t.Errorf("value = %q, want %q", got, "whisper-1")
	}

	// 复合主键必须生效：同键二次插入应触发冲突，上层 Upsert 依赖此行为做覆盖写
	if _, err := sqlxDB.Exec(ins, "global", "", "voice.model", "other", int64(2)); err == nil {
		t.Fatal("同一 (scope, scope_id, name) 重复插入应违反主键约束")
	}
}

// TestDDLStatements_SettingsTable 对三个方言的 settings 建表语句做纯字符串检查，
// 兜住本地跑不到的 MySQL/PG 路径。
func TestDDLStatements_SettingsTable(t *testing.T) {
	for _, d := range []Dialect{DialectSQLite, DialectMySQL, DialectPostgres} {
		var createStmt string
		for _, stmt := range ddlStatements(d) {
			if strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS settings") {
				createStmt = stmt
				break
			}
		}
		if createStmt == "" {
			t.Errorf("dialect %v: 找不到 settings 建表语句", d)
			continue
		}
		if !strings.Contains(createStmt, "PRIMARY KEY (scope, scope_id, name)") {
			t.Errorf("dialect %v: 建表语句缺少复合主键 (scope, scope_id, name):\n%s", d, createStmt)
		}
	}
}

// TestDDLStatements_ModelsHasDefaultFlags 三方言 models 建表语句统一使用整数位掩码列
// default_flags，且不再包含 is_default。
func TestDDLStatements_ModelsHasDefaultFlags(t *testing.T) {
	for _, d := range []Dialect{DialectSQLite, DialectMySQL, DialectPostgres} {
		var createStmt string
		for _, stmt := range ddlStatements(d) {
			if strings.Contains(stmt, "CREATE TABLE IF NOT EXISTS models") {
				createStmt = stmt
				break
			}
		}
		if createStmt == "" {
			t.Errorf("dialect %v: 找不到 models 建表语句", d)
			continue
		}
		if !strings.Contains(createStmt, "default_flags         INTEGER NOT NULL DEFAULT 0") {
			t.Errorf("dialect %v: models 应含 default_flags INTEGER 列:\n%s", d, createStmt)
		}
		if strings.Contains(createStmt, "is_default") {
			t.Errorf("dialect %v: models 不应再含 is_default 列:\n%s", d, createStmt)
		}
	}
}

// extractCreateTable 返回 ddl 中建 table 表的那条 CREATE TABLE 语句；找不到返回空串。
func extractCreateTable(ddl, table string) string {
	marker := "CREATE TABLE IF NOT EXISTS " + table + " ("
	start := strings.Index(ddl, marker)
	if start < 0 {
		return ""
	}
	rest := ddl[start:]
	// 建表语句以独占一行的 ")" 结束（缩进两个 tab）
	if end := strings.Index(rest, "\n\t\t)"); end >= 0 {
		return rest[:end+4]
	}
	return rest
}

// assertColumnOrder 断言 stmt 中 a、b、c 三个列名都出现，且按 a < b < c 的位置顺序排列。
func assertColumnOrder(t *testing.T, d Dialect, stmt, a, b, c string) {
	t.Helper()
	ia, ib, ic := strings.Index(stmt, a), strings.Index(stmt, b), strings.Index(stmt, c)
	if ia < 0 || ib < 0 || ic < 0 {
		t.Errorf("dialect %v: 建表语句缺少列 (%s=%d, %s=%d, %s=%d):\n%s", d, a, ia, b, ib, c, ic, stmt)
		return
	}
	if !(ia < ib && ib < ic) {
		t.Errorf("dialect %v: 列顺序应为 %s < %s < %s，实际位置 %d, %d, %d:\n%s", d, a, b, c, ia, ib, ic, stmt)
	}
}

// TestDDLStatements_TenantColumnsOrder 三方言的 memory_sessions 应含 tenant_id 列
// （位于 session_id 与 user_id 之间），api_keys 应含 tenant 列（位于 name 与 permissions
// 之间），且存在 tenant/user 复合索引。纯字符串检查，兜住本地跑不到的 MySQL/PG 路径。
func TestDDLStatements_TenantColumnsOrder(t *testing.T) {
	for _, d := range []Dialect{DialectSQLite, DialectMySQL, DialectPostgres} {
		ddl := strings.Join(ddlStatements(d), "\n")
		ms := extractCreateTable(ddl, "memory_sessions")
		if ms == "" {
			t.Errorf("dialect %v: 找不到 memory_sessions 建表语句", d)
		} else {
			assertColumnOrder(t, d, ms, "session_id", "tenant_id", "user_id")
		}
		ak := extractCreateTable(ddl, "api_keys")
		if ak == "" {
			t.Errorf("dialect %v: 找不到 api_keys 建表语句", d)
		} else {
			assertColumnOrder(t, d, ak, "name", "tenant_id", "permissions")
		}
		if !strings.Contains(ddl, "idx_ms_tenant_user") && !strings.Contains(ddl, "idx_tenant_user") {
			t.Errorf("dialect %v: missing tenant/user composite index", d)
		}
	}
}

// TestDDLStatements_TitleColumnOrder 三方言的 memory_sessions 应含 title 列，
// 且位于 user_id 与 prompt 之间。纯字符串检查，兜住本地跑不到的 MySQL/PG 路径。
func TestDDLStatements_TitleColumnOrder(t *testing.T) {
	for _, d := range []Dialect{DialectSQLite, DialectMySQL, DialectPostgres} {
		ddl := strings.Join(ddlStatements(d), "\n")
		ms := extractCreateTable(ddl, "memory_sessions")
		if ms == "" {
			t.Errorf("dialect %v: 找不到 memory_sessions 建表语句", d)
			continue
		}
		assertColumnOrder(t, d, ms, "user_id", "title", "prompt")
	}
}

// TestMigrate_FreshDBHasTenantColumns 新库走建表 DDL 路径，断言建出来就带租户列。
func TestMigrate_FreshDBHasTenantColumns(t *testing.T) {
	sqlxDB, dialect, err := Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlxDB.Close()
	if dialect != DialectSQLite {
		t.Fatalf("dialect = %v, want DialectSQLite", dialect)
	}

	for _, c := range []struct{ table, col string }{
		{"memory_sessions", "tenant_id"},
		{"memory_sessions", "title"},
		{"api_keys", "tenant_id"},
	} {
		ok, err := columnExists(sqlxDB, c.table, c.col)
		if err != nil {
			t.Fatalf("columnExists(%s.%s): %v", c.table, c.col, err)
		}
		if !ok {
			t.Errorf("%s.%s missing on fresh DB", c.table, c.col)
		}
	}

	exists, err := indexExists(sqlxDB, "memory_sessions", "idx_ms_tenant_user")
	if err != nil {
		t.Fatalf("indexExists: %v", err)
	}
	if !exists {
		t.Error("index idx_ms_tenant_user missing on fresh DB")
	}
}
