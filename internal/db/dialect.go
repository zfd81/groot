package db

import (
	"fmt"
	"strings"
)

type Dialect int

const (
	DialectSQLite   Dialect = iota
	DialectMySQL
	DialectPostgres
)

func DialectFrom(driver string) Dialect {
	switch driver {
	case "mysql":
		return DialectMySQL
	case "postgres":
		return DialectPostgres
	default:
		return DialectSQLite
	}
}

// Placeholder returns the positional placeholder for a given index (1-based).
// SQLite and MySQL use ?, Postgres uses $1, $2, ...
func (d Dialect) Placeholder(n int) string {
	if d == DialectPostgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// Placeholders returns n consecutive placeholders joined by commas.
func (d Dialect) Placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = d.Placeholder(i + 1)
	}
	return strings.Join(parts, ", ")
}

// UpsertSuffix returns the dialect-specific UPSERT clause appended to a basic
// INSERT statement. conflictCol is the unique column to detect conflicts on;
// updateCols are the columns to overwrite on conflict.
//
// SQLite/Postgres:   ON CONFLICT(<conflictCol>) DO UPDATE SET <c>=excluded.<c>, ...
// MySQL:             ON DUPLICATE KEY UPDATE <c>=VALUES(<c>), ...
func (d Dialect) UpsertSuffix(conflictCol string, updateCols ...string) string {
	if d == DialectMySQL {
		parts := make([]string, len(updateCols))
		for i, c := range updateCols {
			parts[i] = fmt.Sprintf("%s=VALUES(%s)", c, c)
		}
		return "ON DUPLICATE KEY UPDATE " + strings.Join(parts, ", ")
	}
	parts := make([]string, len(updateCols))
	for i, c := range updateCols {
		parts[i] = fmt.Sprintf("%s=excluded.%s", c, c)
	}
	return fmt.Sprintf("ON CONFLICT(%s) DO UPDATE SET %s", conflictCol, strings.Join(parts, ", "))
}

// InsertIgnore returns the dialect-specific INSERT statement that silently
// ignores duplicate key conflicts.
//
// SQLite:    INSERT OR IGNORE INTO <table> (<cols>) VALUES (<placeholders>)
// MySQL:     INSERT IGNORE INTO <table> (<cols>) VALUES (<placeholders>)
// Postgres:  INSERT INTO <table> (<cols>) VALUES (<placeholders>) ON CONFLICT DO NOTHING
//
// cols is the column list; the caller writes the matching placeholders inline.
func (d Dialect) InsertIgnorePrefix() string {
	switch d {
	case DialectMySQL:
		return "INSERT IGNORE INTO"
	case DialectPostgres:
		return "INSERT INTO"
	default:
		return "INSERT OR IGNORE INTO"
	}
}

// InsertIgnoreSuffix returns the trailing clause for postgres-style INSERT
// IGNORE; empty string for SQLite/MySQL which use the prefix form.
func (d Dialect) InsertIgnoreSuffix() string {
	if d == DialectPostgres {
		return "ON CONFLICT DO NOTHING"
	}
	return ""
}
