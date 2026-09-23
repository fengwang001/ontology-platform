package source

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrEnvAmbiguous 表示两个不同的环境变量名映射到同一个键。
var ErrEnvAmbiguous = errors.New("source: ambiguous environment mapping")

// Env 读取 environ（"NAME=VALUE" 列表），取带 prefix 的变量，
// 按两条规则映射为键路径：规则一每个 '_' 是分隔符（A_B_C → a.b.c），
// 规则二下划线连续段折叠为分隔符（A__B_C → a.b.c）。两个不同变量名
// 落到同一个键时报 ErrEnvAmbiguous。
func Env(prefix string, environ []string) ([]Entry, error) {
	names := map[string]map[string]string{} // key -> env name -> value
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		stripped := strings.TrimPrefix(name, prefix)
		for _, key := range candidateKeys(stripped) {
			if names[key] == nil {
				names[key] = map[string]string{}
			}
			names[key][name] = value
		}
	}
	keys := make([]string, 0, len(names))
	for key := range names {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]Entry, 0, len(keys))
	for _, key := range keys {
		vars := names[key]
		if len(vars) > 1 {
			list := make([]string, 0, len(vars))
			for n := range vars {
				list = append(list, n)
			}
			sort.Strings(list)
			return nil, fmt.Errorf("%w: key %q from %s", ErrEnvAmbiguous, key, strings.Join(list, ", "))
		}
		for _, value := range vars {
			entries = append(entries, Entry{Key: key, Value: value, Layer: LayerEnv})
		}
	}
	return entries, nil
}

// candidateKeys 返回变量名在两条规则下的合法候选键（非法键丢弃）。
func candidateKeys(name string) []string {
	rule1 := strings.ToLower(strings.ReplaceAll(name, "_", "."))
	rule2 := strings.ToLower(collapseUnderscores(name))
	seen := map[string]bool{}
	var keys []string
	for _, k := range []string{rule1, rule2} {
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, err := NormalizeKey(k); err == nil {
			keys = append(keys, k)
		}
	}
	return keys
}

func collapseUnderscores(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if r == '_' {
			if !lastUnderscore {
				b.WriteByte('.')
			}
			lastUnderscore = true
			continue
		}
		lastUnderscore = false
		b.WriteRune(r)
	}
	return b.String()
}
