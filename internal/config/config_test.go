package config

import (
	"os"
	"path/filepath"
	"testing"
)

var envKeys = []string{
	"BITBUCKET_SERVER_USER",
	"BITBUCKET_SERVER_TOKEN",
	"BITBUCKET_SERVER_PASSWORD",
	"BITBUCKET_CLOUD_EMAIL",
	"BITBUCKET_CLOUD_API_TOKEN",
	"BITBUCKET_CLOUD_WORKSPACE",
}

// isolateEnv clears the relevant vars for the test and restores them after, so
// the package-level os.Setenv performed by dotenv loading cannot leak.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		if v, ok := os.LookupEnv(k); ok {
			old := v
			t.Cleanup(func() { os.Setenv(k, old) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
		os.Unsetenv(k)
	}
}

func writeFixture(t *testing.T) (cfgPath, envPath string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "projects.yaml")
	envPath = filepath.Join(dir, "custom.env")

	must(t, os.WriteFile(cfgPath, []byte("source:\n  base_url: https://bb.example.com\ntarget:\n  workspace: ws\nprojects:\n  - key: XOPS\n"), 0o600))
	must(t, os.WriteFile(envPath, []byte(
		"BITBUCKET_SERVER_USER=u\n"+
			"BITBUCKET_SERVER_TOKEN=tok\n"+
			"BITBUCKET_CLOUD_EMAIL=e@example.com\n"+
			"BITBUCKET_CLOUD_API_TOKEN=api\n"), 0o600))
	return cfgPath, envPath
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadWithEnvExplicitPath(t *testing.T) {
	isolateEnv(t)
	cfgPath, envPath := writeFixture(t)

	cfg, err := LoadWithEnv(cfgPath, envPath)
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	if cfg.Source.User != "u" || cfg.Source.Token != "tok" {
		t.Errorf("source = %+v", cfg.Source)
	}
	if cfg.Target.Email != "e@example.com" || cfg.Target.APIToken != "api" {
		t.Errorf("target = %+v", cfg.Target)
	}
}

func TestLoadWithEnvMissingFileErrors(t *testing.T) {
	isolateEnv(t)
	cfgPath, _ := writeFixture(t)

	missing := filepath.Join(t.TempDir(), "nope.env")
	if _, err := LoadWithEnv(cfgPath, missing); err == nil {
		t.Fatal("expected error for missing -env file")
	}
}

func TestLoadWithEnvRealEnvWins(t *testing.T) {
	isolateEnv(t)
	cfgPath, envPath := writeFixture(t)
	t.Setenv("BITBUCKET_CLOUD_WORKSPACE", "real-ws")

	cfg, err := LoadWithEnv(cfgPath, envPath)
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	if cfg.Target.Workspace != "real-ws" {
		t.Errorf("workspace = %q, want real-ws", cfg.Target.Workspace)
	}
}

func TestLoadWithOptionalDefaultEnvAbsent(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "projects.yaml")
	must(t, os.WriteFile(cfgPath, []byte("source:\n  base_url: https://bb.example.com\nprojects:\n  - key: XOPS\n"), 0o600))
	t.Setenv("BITBUCKET_SERVER_TOKEN", "tok")
	t.Setenv("BITBUCKET_CLOUD_EMAIL", "e@example.com")
	t.Setenv("BITBUCKET_CLOUD_API_TOKEN", "api")
	t.Setenv("BITBUCKET_CLOUD_WORKSPACE", "ws")

	// No ./.env in the test's working directory; must not error.
	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load without .env: %v", err)
	}
}
