package storage

import (
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

func TestFactory_MinioMissingFieldsErrors(t *testing.T) {
	cfg := config.StorageConfig{Minio: &config.MinioConfig{
		Endpoint:  "",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "groot",
	}}
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestFactory_MinioExpandsEnvVars(t *testing.T) {
	t.Setenv("MINIO_AK_TEST", "expanded-ak")
	t.Setenv("MINIO_SK_TEST", "expanded-sk")
	cfg := config.StorageConfig{Minio: &config.MinioConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "${MINIO_AK_TEST}",
		SecretKey: "${MINIO_SK_TEST}",
		Bucket:    "groot",
	}}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New with env-expanded creds: %v", err)
	}
	if _, ok := s.(*Minio); !ok {
		t.Fatalf("expected *Minio, got %T", s)
	}
}

func TestFactory_MinioEmptyEnvVarErrors(t *testing.T) {
	// 不设置 env 变量，让 ExpandEnv 返回空串
	cfg := config.StorageConfig{Minio: &config.MinioConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "${MINIO_AK_DEFINITELY_NOT_SET_XYZ}",
		SecretKey: "${MINIO_SK_DEFINITELY_NOT_SET_XYZ}",
		Bucket:    "groot",
	}}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error when env vars expand to empty string")
	}
}
