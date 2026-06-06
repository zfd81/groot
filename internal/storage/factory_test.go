package storage

import (
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/config"
)

func TestFactory_NoMinioYieldsLocal(t *testing.T) {
	s, err := New(config.StorageConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := s.(*Local); !ok {
		t.Fatalf("expected *Local, got %T", s)
	}
}

func TestFactory_WithMinioYieldsMinio(t *testing.T) {
	cfg := config.StorageConfig{Minio: &config.MinioConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "groot",
	}}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := s.(*Minio); !ok {
		t.Fatalf("expected *Minio, got %T", s)
	}
}

// TestFactory_MinioMissingFieldsErrors 用 table-driven 覆盖 4 种缺失字段：
// endpoint / bucket / access_key / secret_key。
func TestFactory_MinioMissingFieldsErrors(t *testing.T) {
	base := config.MinioConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "groot",
	}
	cases := []struct {
		name      string
		mut       func(*config.MinioConfig)
		wantInErr string
	}{
		{"missing endpoint", func(m *config.MinioConfig) { m.Endpoint = "" }, "endpoint"},
		{"missing bucket", func(m *config.MinioConfig) { m.Bucket = "" }, "bucket"},
		{"missing access_key", func(m *config.MinioConfig) { m.AccessKey = "" }, "access_key"},
		{"missing secret_key", func(m *config.MinioConfig) { m.SecretKey = "" }, "secret_key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mut(&cfg)
			_, err := New(config.StorageConfig{Minio: &cfg})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantInErr)
			}
		})
	}
}
