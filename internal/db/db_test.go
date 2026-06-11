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
		"memory_sessions", "memory_chats", "shared_resources",
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
