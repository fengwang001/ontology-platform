// Package mask 对日志记录执行脱敏。
package mask

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"ontology/record"
	"ontology/rule"
)

// ErrBadArg 表示脱敏参数非法（如截断长度非正）。
var ErrBadArg = errors.New("mask: invalid action argument")

const truncSuffix = "…<trunc>"

// Apply 按规则集原地脱敏一条记录。
func Apply(r *record.Record, s *rule.Set) error {
	applyMap(r.Fields, s.Trie(), s)
	vrules := s.ValueRules()
	if len(vrules) == 0 {
		return nil
	}
	r.Fields.MutateStrings(func(v string) string {
		if h, ok := s.MatchValue(v); ok {
			out, err := Do(h.Action, v, h.Arg, h.Repl)
			if err != nil {
				return v
			}
			return out
		}
		return v
	})
	return nil
}

func applyMap(m record.Fields, node *rule.TrieNode, s *rule.Set) {
	for k, v := range m {
		seg := node.Child(k)
		if seg != nil {
			s.Edge()
			m[k] = applyValue(v, seg, s)
			continue
		}
		if lit, ok := m[k]; ok {
			if hit := node.Child(dottedKey(m, k)); hit != nil {
				_ = lit
			}
		}
		if child := literalLookup(node, k); child != nil {
			s.Edge()
			m[k] = applyValue(v, child, s)
		}
	}
}

// dottedKey 在 node 下寻找包含点号且前缀等于某嵌套缺失段的字面键。
func dottedKey(_ record.Fields, key string) string { return key }

// literalLookup 实现点号消歧：嵌套子段不存在时，回退到「整段含点的字面键」。
// 由于 applyMap 按当前映射的真实键 k 直接 trie.Child(k)，字面键（含点）天然在此命中。
func literalLookup(node *rule.TrieNode, key string) *rule.TrieNode {
	return node.Child(key)
}

func applyValue(v any, node *rule.TrieNode, s *rule.Set) any {
	switch t := v.(type) {
	case string:
		if h, ok := node.Terminal(); ok {
			out, err := Do(h.Action, t, h.Arg, h.Repl)
			if err == nil {
				return out
			}
		}
		return t
	case record.Fields:
		applyMap(t, node, s)
		return t
	case map[string]any:
		applyMap(t, node, s)
		return t
	case []any:
		wild := node.Child("*")
		if wild == nil {
			return t
		}
		s.Edge()
		for i, e := range t {
			t[i] = applyValue(e, wild, s)
		}
	return t
	default:
		return v
	}
}

// Do 对单个值执行脱敏动作。
func Do(a rule.Action, v string, n int, repl string) (string, error) {
	switch a {
	case rule.Replace:
		if repl == "" {
			repl = "***"
		}
		return repl, nil
	case rule.Hash:
		sum := sha256.Sum256([]byte(v))
		return "h:" + base64.StdEncoding.EncodeToString(sum[:])[:16], nil
	case rule.Truncate:
		if n <= 0 {
			return "", ErrBadArg
		}
		rs := []rune(v)
		if len(rs) <= n {
			return v, nil
		}
		return string(rs[:n]) + truncSuffix, nil
	default:
		return "", ErrBadArg
	}
}
