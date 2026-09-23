package source

import (
	"errors"
	"fmt"
	"strings"
)

var ErrAmbiguousEnv = errors.New("ambiguous environment variable mapping")

// EnvName maps a dotted key to its canonical environment variable name:
// uppercase with dots replaced by single underscores (a.b.c -> A_B_C).
func EnvName(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// envKey normalizes an environment variable name back to a key: lowercase,
// with runs of underscores collapsed into a single dot.
func envKey(name string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(name) {
		if r == '_' {
			if !prevUnderscore {
				b.WriteByte('.')
			}
			prevUnderscore = true
			continue
		}
		prevUnderscore = false
		b.WriteRune(r)
	}
	return b.String()
}

// FromEnv normalizes "NAME=value" pairs into entries. Only names with the
// given prefix are considered (empty prefix accepts all). Two distinct
// variable names normalizing to the same key are reported as ambiguous.
func FromEnv(layer Layer, environ []string, prefix string) ([]Entry, error) {
	seen := make(map[string]string) // normalized key -> original name
	out := make([]Entry, 0, len(environ))
	for _, pair := range environ {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("env entry %q: want NAME=value", pair)
		}
		name := kv[0]
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		key := envKey(name)
		if err := CheckKey(key); err != nil {
			return nil, err
		}
		if prev, ok := seen[key]; ok && prev != name {
			return nil, fmt.Errorf("%w: %s and %s both map to %q",
				ErrAmbiguousEnv, prev, name, key)
		}
		seen[key] = name
		out = append(out, Entry{Key: key, Value: kv[1], Layer: layer})
	}
	return out, nil
}
