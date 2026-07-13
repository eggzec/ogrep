package xdg

import (
	"os"

	"github.com/BurntSushi/toml"
)

// Config is officegrep's optional user config file, read from
// ConfigFilePath() ("$XDG_CONFIG_HOME/officegrep/config.toml") if
// present. It seeds CLI defaults; explicit command-line flags always
// override values loaded from here.
//
// This is intentionally a small proof-of-mechanism, covering: default
// color mode, default thread count, and extra ignore globs. Later
// phases can grow it as more flags gain config-file equivalents.
type Config struct {
	Color   string   `toml:"color"`
	Threads int      `toml:"threads"`
	Ignore  []string `toml:"ignore"`
}

// LoadConfig reads and parses the TOML config file at ConfigFilePath().
// officegrep never auto-creates this file: if it doesn't exist,
// LoadConfig returns a zero-value Config and a nil error, so callers
// can treat "no config" identically to "empty config".
func LoadConfig() (Config, error) {
	var cfg Config
	path := ConfigFilePath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
