package config

import (
	"fmt"
	"os"
	"regexp"
)

// envPlaceholder matches `{{ env "NAME" }}` (whitespace inside the braces is
// optional). Non-greedy on the name so several placeholders on one line are
// each expanded on their own.
var envPlaceholder = regexp.MustCompile(`\{\{\s*env\s+"([^"]+)"\s*\}\}`)

// ExpandEnv replaces every `{{ env "NAME" }}` placeholder in raw with the
// value of the environment variable NAME. A placeholder naming an unset
// variable is an error rather than an empty string, so a missing secret or
// address can't silently produce a broken Envoy resource.
func ExpandEnv(raw []byte) ([]byte, error) {
	var missing []string

	out := envPlaceholder.ReplaceAllFunc(raw, func(m []byte) []byte {
		name := string(envPlaceholder.FindSubmatch(m)[1])
		val, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
			return m
		}
		return []byte(val)
	})

	if len(missing) > 0 {
		return nil, fmt.Errorf("environment variable(s) not set: %v", missing)
	}
	return out, nil
}
