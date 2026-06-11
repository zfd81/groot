package config

import (
	"testing"
)

func TestDatabaseConfig_DefaultIsNil(t *testing.T) {
	var c Config
	if c.Database != nil {
		t.Fatal("default Config.Database should be nil (no database configured)")
	}
}

func TestDatabaseConfig_Fields(t *testing.T) {
	db := &DatabaseConfig{
		Driver:          "sqlite",
		DSN:             "/tmp/groot.db",
		MaxOpenConns:    20,
		MaxIdleConns:    5,
		ConnMaxLifetime: "30m",
	}
	if db.Driver != "sqlite" {
		t.Errorf("Driver = %q", db.Driver)
	}
	if db.MaxOpenConns != 20 {
		t.Errorf("MaxOpenConns = %d", db.MaxOpenConns)
	}
	if db.ConnMaxLifetime != "30m" {
		t.Errorf("ConnMaxLifetime = %q", db.ConnMaxLifetime)
	}
}
