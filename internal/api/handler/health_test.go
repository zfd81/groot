package handler

import (
	"testing"

	"github.com/zfd81/groot/internal/config"
)

func TestDatabaseType(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.DatabaseConfig
		want string
	}{
		{"nil 配置默认 sqlite", nil, "sqlite"},
		{"空 driver 默认 sqlite", &config.DatabaseConfig{}, "sqlite"},
		{"sqlite", &config.DatabaseConfig{Driver: "sqlite"}, "sqlite"},
		{"mysql", &config.DatabaseConfig{Driver: "mysql"}, "mysql"},
		{"postgres", &config.DatabaseConfig{Driver: "postgres"}, "postgres"},
		{"未知 driver 回落 sqlite", &config.DatabaseConfig{Driver: "oracle"}, "sqlite"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := databaseType(c.cfg); got != c.want {
				t.Errorf("databaseType(%+v) = %q, want %q", c.cfg, got, c.want)
			}
		})
	}
}
