package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStorageConfig_DefaultIsLocal(t *testing.T) {
	var c Config
	if c.Storage.Minio != nil {
		t.Fatal("default Storage.Minio should be nil (local mode)")
	}
}

func TestStorageConfig_ParsesMinioBlock(t *testing.T) {
	src := `
storage:
  minio:
    endpoint: localhost:9000
    access_key: ak
    secret_key: sk
    bucket: groot
    use_ssl: true
`
	var c Config
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Storage.Minio == nil {
		t.Fatal("expected Storage.Minio to be set")
	}
	if c.Storage.Minio.Endpoint != "localhost:9000" {
		t.Errorf("endpoint = %q", c.Storage.Minio.Endpoint)
	}
	if !c.Storage.Minio.UseSSL {
		t.Error("UseSSL should be true")
	}
}

func TestStorageConfig_OmittedYieldsLocal(t *testing.T) {
	src := strings.TrimSpace(`
agent:
  name: groot
`)
	var c Config
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Storage.Minio != nil {
		t.Fatal("Storage.Minio should be nil when storage block omitted")
	}
}
