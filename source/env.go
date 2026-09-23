package source

import (
	"sort"
	"strings"
)

func flatName(key string) string {
	parts := strings.Split(key, ".")
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(strings.ToUpper(part), "_", "__")
	}
	return strings.Join(parts, "_")
}

func escapedName(key string) string {
	upper := strings.ToUpper(key)
	first := strings.IndexByte(upper, '.')
	if first < 0 {
		return upper
	}
	return upper[:first] + "__" + strings.ReplaceAll(upper[first+1:], ".", "_")
}

func FromEnv(env map[string]string, keys []string) (Layer, error) {
	values := make(map[string]string)
	ambiguous := make(map[string]struct{})
	sortedKeys := append([]string(nil), keys...)
	sort.Strings(sortedKeys)
	for _, key := range sortedKeys {
		if err := ValidateKey(key); err != nil {
			return Layer{}, err
		}
		flat, hasFlat := lookupEnv(env, flatName(key))
		escaped, hasEscaped := lookupEnv(env, escapedName(key))
		if hasFlat && hasEscaped && flat != escaped {
			ambiguous[key] = struct{}{}
			continue
		}
		if hasFlat {
			values[key] = flat
		} else if hasEscaped {
			values[key] = escaped
		}
	}
	if len(ambiguous) > 0 {
		names := make([]string, 0, len(ambiguous))
		for key := range ambiguous {
			names = append(names, key)
		}
		sort.Strings(names)
		return Layer{}, &AmbiguousEnvError{Names: names}
	}
	return NewLayer(Env, values)
}

func lookupEnv(env map[string]string, name string) (string, bool) {
	value, ok := env[name]
	return value, ok
}

func FromFlags(flags map[string]string) (Layer, error) {
	return NewLayer(Flag, flags)
}

func FromDefaults(defaults map[string]string) (Layer, error) {
	return NewLayer(Default, defaults)
}
