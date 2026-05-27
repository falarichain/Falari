package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration for human-readable YAML parsing (e.g. "5s", "10m", "1h").
type Duration time.Duration

// Duration returns the underlying time.Duration value.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses a YAML string node into a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(dur)
	return nil
}

// MarshalYAML serializes a Duration to its string representation.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}
