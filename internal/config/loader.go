package config

import (
	"flag"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads a YAML config file and unmarshals it into cfg.
// cfg should be pre-populated with default values before calling Load.
func Load(path string, cfg any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, cfg)
}

// IsFlagSet returns true if the named flag was explicitly set on the command line.
// It uses flag.Visit which only iterates over flags that have been set.
func IsFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
