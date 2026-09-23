// Package source reads and normalizes configuration entries from four
// kinds of origins: defaults, files, environment variables and CLI args.
package source

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Layer identifies the priority tier an entry came from.
type Layer int

const (
	Default Layer = iota
	File
	Env
	CLI
)

func (l Layer) String() string {
	switch l {
	case Default:
		return "default"
	case File:
		return "file"
	case Env:
		return "env"
	case CLI:
		return "cli"
	}
	return "unknown"
}

// Entry is one normalized configuration item: key path + raw value + origin.
type Entry struct {
	Key   string
	Value string
	Layer Layer
}

var (
	ErrEmptyKey     = errors.New("empty key")
	ErrEmptySegment = errors.New("empty key path segment")
)

// CheckKey validates a dotted key path.
func CheckKey(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	for _, seg := range strings.Split(key, ".") {
		if seg == "" {
			return fmt.Errorf("%w in %q", ErrEmptySegment, key)
		}
	}
	return nil
}

// FromPairs normalizes an in-memory key/value map into one layer of entries.
// Keys are validated and the output is sorted for determinism.
func FromPairs(layer Layer, pairs map[string]string) ([]Entry, error) {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		if err := CheckKey(k); err != nil {
			return nil, err
		}
		out = append(out, Entry{Key: k, Value: pairs[k], Layer: layer})
	}
	return out, nil
}

// ParseArgs normalizes CLI arguments of the form --key=value.
func ParseArgs(layer Layer, args []string) ([]Entry, error) {
	out := make([]Entry, 0, len(args))
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			return nil, fmt.Errorf("cli arg %q: missing -- prefix", a)
		}
		kv := strings.SplitN(a[2:], "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("cli arg %q: want --key=value", a)
		}
		if err := CheckKey(kv[0]); err != nil {
			return nil, err
		}
		out = append(out, Entry{Key: kv[0], Value: kv[1], Layer: layer})
	}
	return out, nil
}
