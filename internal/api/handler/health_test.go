package handler

import (
	"os"
	"strconv"
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

func TestEnvironmentInfo(t *testing.T) {
	h := &HealthHandler{
		config:      config.Config{Logging: config.LoggingConfig{File: config.LogFileConfig{Directory: "/var/log/groot"}}},
		homeDir:     "/home/x/.groot",
		processMode: "supervised",
	}
	info := h.environmentInfo()
	if info["home_dir"] != "/home/x/.groot" {
		t.Errorf("home_dir = %q", info["home_dir"])
	}
	if info["log_dir"] != "/var/log/groot" {
		t.Errorf("log_dir = %q", info["log_dir"])
	}
	if info["database"] != "sqlite" {
		t.Errorf("database = %q, want sqlite（配置缺省）", info["database"])
	}
	if info["process_mode"] != "supervised" {
		t.Errorf("process_mode = %q", info["process_mode"])
	}
	if info["pid"] != strconv.Itoa(os.Getpid()) {
		t.Errorf("pid = %q, want %d", info["pid"], os.Getpid())
	}
}
