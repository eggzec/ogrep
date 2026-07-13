package xdg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDirUsesEnvWhenAbsolute(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	want := filepath.Join("/custom/config", "ogrep")
	if got := ConfigDir(); got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestConfigDirFallsBackWhenEnvRelative(t *testing.T) {
	// Per the XDG Base Directory spec, a relative value must be treated
	// as if the variable were unset.
	t.Setenv("XDG_CONFIG_HOME", "relative/path")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available in this environment")
	}
	want := filepath.Join(home, ".config", "ogrep")
	if got := ConfigDir(); got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestConfigDirFallsBackWhenEnvUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available in this environment")
	}
	want := filepath.Join(home, ".config", "ogrep")
	if got := ConfigDir(); got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestCacheDirUsesEnvWhenAbsolute(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	want := filepath.Join("/custom/cache", "ogrep")
	if got := CacheDir(); got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDirFallsBackWhenEnvRelative(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "relative")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available in this environment")
	}
	want := filepath.Join(home, ".cache", "ogrep")
	if got := CacheDir(); got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
}

func TestDataDirUsesEnvWhenAbsolute(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	want := filepath.Join("/custom/data", "ogrep")
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDirFallsBackWhenEnvRelative(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "not/absolute")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available in this environment")
	}
	want := filepath.Join(home, ".local", "share", "ogrep")
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestConfigFilePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	want := filepath.Join("/custom/config", "ogrep", "config.toml")
	if got := ConfigFilePath(); got != want {
		t.Errorf("ConfigFilePath() = %q, want %q", got, want)
	}
}

func TestLoadConfigMissingFileReturnsZeroValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty dir, no config.toml
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil for a missing file", err)
	}
	if cfg.Color != "" || cfg.Threads != 0 || len(cfg.Ignore) != 0 {
		t.Errorf("LoadConfig() = %+v, want zero value", cfg)
	}
}

func TestLoadConfigParsesTOML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	confDir := filepath.Join(dir, "ogrep")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `
color = "always"
threads = 4
ignore = ["*.bak", "vendor/**"]
`
	if err := os.WriteFile(filepath.Join(confDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Color != "always" || cfg.Threads != 4 || len(cfg.Ignore) != 2 {
		t.Errorf("LoadConfig() = %+v, unexpected values", cfg)
	}
}
