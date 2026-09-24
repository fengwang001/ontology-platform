// Package mask 对结构化记录执行脱敏：替换 / 哈希 / 截断。
package mask

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"ontology/record"
	"ontology/rule"
)

// ReplaceStr 是替换动作的固定串（长度不保留）。
const ReplaceStr = "***REDACTED***"

// TruncSuffix 标注字段被截断。
const TruncSuffix = "…(truncated)"

// Masker 持有预编译规则集；只读，可被多 goroutine 并发使用。
type Masker struct {
	set *rule.Set
}

// New 构造 Masker。
func New(set *rule.Set) *Masker { return &Masker{set: set} }

// Apply 原地重建记录字段并脱敏，返回记录自身。
func (m *Masker) Apply(rec *record.Record) *record.Record {
	targets := map[string]*rule.Rule{}
	for _, h := range m.set.Walk(rec.Fields) {
		targets[strings.Join(h.Segments, "\x00")] = h.Rule
	}
	rec.Fields = m.maskMap(rec.Fields, nil, targets)
	return rec
}

func (m *Masker) maskMap(in map[string]any, path []string, targets map[string]*rule.Rule) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		cp := append(append([]string{}, path...), k)
		key := strings.Join(cp, "\x00")
		if r, ok := targets[key]; ok {
			out[k] = maskValue(v, r)
			continue
		}
		out[k] = m.maskAny(v, cp, targets)
	}
	return out
}

func (m *Masker) maskAny(v any, path []string, targets map[string]*rule.Rule) any {
	switch t := v.(type) {
	case map[string]any:
		return m.maskMap(t, path, targets)
	case []any:
		out := make([]any, len(t))
		// 路径规则不进入数组；数组内字符串由值模式覆盖。
		for i, e := range t {
			out[i] = m.maskAny(e, path, targets)
		}
		return out
	case string:
		for _, r := range m.set.ValueRules() {
			if r.Pattern.MatchString(t) {
				return maskValue(t, r)
			}
		}
		return t
	default:
		return v
	}
}

// maskValue 按规则动作产出脱敏值。
func maskValue(v any, r *rule.Rule) any {
	s, isStr := v.(string)
	if !isStr {
		s = fmt.Sprint(v)
	}
	switch r.Action {
	case rule.Hash:
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	case rule.Truncate:
		runes := []rune(s)
		n := r.KeepN
		if n > len(runes) {
			n = len(runes)
		}
		if n < 0 {
			n = 0
		}
		return string(runes[:n]) + TruncSuffix
	default:
		return ReplaceStr
	}
}

// HashString 暴露哈希供外部断言一致性。
func HashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
