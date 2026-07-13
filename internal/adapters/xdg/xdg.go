// Package xdg resolves paths per the XDG Base Directory specification
// (https://specifications.freedesktop.org/basedir-spec/latest/) and
// loads ogrep's optional TOML config file from the resolved
// config directory.
//
// Per spec: if an XDG_*_HOME environment variable is set but its value
// is not an absolute path, it must be treated as unset and the default
// fallback used instead.
package xdg

import (
	"os"
	"path/filepath"
)

const appName = "ogrep"

// envOrFallback returns the value of the named environment variable if
// it is set AND an absolute path, otherwise it returns fallback.
func envOrFallback(envVar, fallback string) string {
	v := os.Getenv(envVar)
	if v != "" && filepath.IsAbs(v) {
		return v
	}
	return fallback
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// ConfigHome returns the resolved XDG_CONFIG_HOME base directory
// (without the application-specific suffix).
func ConfigHome() string {
	return envOrFallback("XDG_CONFIG_HOME", filepath.Join(homeDir(), ".config"))
}

// CacheHome returns the resolved XDG_CACHE_HOME base directory (without
// the application-specific suffix).
func CacheHome() string {
	return envOrFallback("XDG_CACHE_HOME", filepath.Join(homeDir(), ".cache"))
}

// DataHome returns the resolved XDG_DATA_HOME base directory (without
// the application-specific suffix).
func DataHome() string {
	return envOrFallback("XDG_DATA_HOME", filepath.Join(homeDir(), ".local", "share"))
}

// ConfigDir returns "$XDG_CONFIG_HOME/ogrep". ogrep does not
// create this directory; it only reads config.toml from it if present.
func ConfigDir() string {
	return filepath.Join(ConfigHome(), appName)
}

// CacheDir returns "$XDG_CACHE_HOME/ogrep". Unused in v1 (there is
// no cache yet), but resolvable so future phases have a stable path to
// build on. ogrep does not create this directory.
func CacheDir() string {
	return filepath.Join(CacheHome(), appName)
}

// DataDir returns "$XDG_DATA_HOME/ogrep". ogrep does not
// create this directory.
func DataDir() string {
	return filepath.Join(DataHome(), appName)
}

// ConfigFilePath returns the path ogrep reads its optional TOML
// config from: "$XDG_CONFIG_HOME/ogrep/config.toml".
func ConfigFilePath() string {
	return filepath.Join(ConfigDir(), "config.toml")
}
