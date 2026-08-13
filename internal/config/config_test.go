package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadRPCUsesTypedDefaultsAndEnvironment(t *testing.T) {
	path := writeConfig(t, "db:\n  dsn: test-dsn\n")
	t.Setenv("GRPC_ADDR", ":6000")
	t.Setenv("CACHE_DEFAULT_TTL", "2h")
	t.Setenv("ETCD_ENDPOINTS", "etcd-a:2379,etcd-b:2379")

	cfg, err := LoadRPC(path)
	if err != nil {
		t.Fatalf("LoadRPC() error: %v", err)
	}
	if cfg.DB.DSN != "test-dsn" || cfg.GRPC.Addr != ":6000" {
		t.Fatalf("RPC endpoints = dsn %q grpc %q", cfg.DB.DSN, cfg.GRPC.Addr)
	}
	if cfg.Cache.DefaultTTL != 2*time.Hour || cfg.Cache.LookupTimeout != 3*time.Second {
		t.Fatalf("cache durations = default %s lookup %s", cfg.Cache.DefaultTTL, cfg.Cache.LookupTimeout)
	}
	if len(cfg.Etcd.Endpoints) != 2 || cfg.Etcd.Endpoints[1] != "etcd-b:2379" {
		t.Fatalf("etcd endpoints = %#v", cfg.Etcd.Endpoints)
	}
}

func TestLoadWebRejectsEmptyAPIKeyWhenAuthEnabled(t *testing.T) {
	path := writeConfig(t, "auth:\n  enabled: true\n  api_key: \"\"\n")

	_, err := LoadWeb(path)
	if err == nil {
		t.Fatal("LoadWeb() expected validation error")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
