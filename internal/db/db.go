package db

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"github.com/zfd81/groot/internal/config"
)

// Open opens a database connection, runs migrations, and returns the db.
// If cfg is nil, opens SQLite at homeDir/groot.db.
func Open(cfg *config.DatabaseConfig, homeDir string) (*sqlx.DB, Dialect, error) {
	driver, dsn, dialect := resolveDriver(cfg, homeDir)

	db, err := sqlx.Open(driver, dsn)
	if err != nil {
		return nil, dialect, fmt.Errorf("db open: %w", err)
	}

	maxOpen := 20
	maxIdle := 5
	maxLife := 30 * time.Minute

	if cfg != nil {
		if cfg.MaxOpenConns > 0 {
			maxOpen = cfg.MaxOpenConns
		}
		if cfg.MaxIdleConns > 0 {
			maxIdle = cfg.MaxIdleConns
		}
		if cfg.ConnMaxLifetime != "" {
			if d, err := time.ParseDuration(cfg.ConnMaxLifetime); err == nil {
				maxLife = d
			}
		}
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLife)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, dialect, fmt.Errorf("db ping: %w", err)
	}

	if err := Migrate(db, dialect); err != nil {
		db.Close()
		return nil, dialect, fmt.Errorf("db migrate: %w", err)
	}

	return db, dialect, nil
}

func resolveDriver(cfg *config.DatabaseConfig, homeDir string) (driver, dsn string, dialect Dialect) {
	if cfg == nil {
		dbPath := filepath.Join(homeDir, "groot.db")
		return "sqlite3", dbPath + "?_journal_mode=WAL&_busy_timeout=5000", DialectSQLite
	}
	d := cfg.Driver
	dsn = os.ExpandEnv(cfg.DSN)
	dialect = DialectFrom(d)
	switch dialect {
	case DialectMySQL:
		driver = "mysql"
	case DialectPostgres:
		driver = "postgres"
	default:
		driver = "sqlite3"
	}
	return driver, dsn, dialect
}
