// Package mask 在记录副本上执行脱敏：替换 / 哈希 / 截断，支持路径与按值模式。
package mask

import (
	"crypto/sha256"
	"encoding/hex"

	"ontology/record"
	"ontology/rule"
)

const truncSuffix = "…(truncated)"

// Apply 返回脱敏后的新记录（深拷贝），原记录不被修改。
// 遍历与规则 trie 同步下钻。消歧在进入一个 map 前一次性完成（与遍历顺序无关）：
// 若某条规则的“整键字面量”在本 map 真实存在，则该规则的嵌套解释在对应子树被抑制。
func Apply(r *record.Record, set *rule.Set) *record.Record {
	out := r.Clone()
	out.Fields = walkMap(out.Fields, set.Root(), set, map[*rule.Node]bool{}).(map[string]any)
	return out
}

func walkMap(m map[string]any, n *rule.Node, set *rule.Set, blocked map[*rule.Node]bool) any {
	type entry struct {
		key   string
		child *rule.Node
		lit   bool
	}
	entries := make([]entry, 0, len(m))
	childBlocked := map[*rule.Node]bool{}
	for t := range blocked {
		childBlocked[t] = true
	}
	for key := range m {
		child, isLiteral := set.MatchKey(n, key)
		if isLiteral {
			// 字面量优先：沿别名段构造每个嵌套子树的抑制标记（目标终端节点）。
			if av, ok := n.Aliases()[key]; ok {
				suppress(n, av.Segs(), set, childBlocked)
			}
		}
		entries = append(entries, entry{key, child, isLiteral})
	}
	for _, e := range entries {
		m[e.key] = process(m[e.key], e.child, set, childBlocked)
	}
	return m
}

// suppress 按别名段下钻，把目标终端节点登记到“被整键占用”的子树节点集合。
func suppress(parent *rule.Node, segs []string, set *rule.Set, blocked map[*rule.Node]bool) {
	cur := parent
	for _, seg := range segs {
		cur = cur.Child(seg)
		if cur == nil {
			return
		}
	}
	blocked[cur] = true
}

func walkSlice(arr []any, n *rule.Node, set *rule.Set, blocked map[*rule.Node]bool) any {
	for i, raw := range arr {
		arr[i] = process(raw, set.ArrayEnter(n, i), set, blocked)
	}
	return arr
}

// process 处理一个值：n 为该位置的规则节点（可能 nil）。
func process(v any, n *rule.Node, set *rule.Set, blocked map[*rule.Node]bool) any {
	switch t := v.(type) {
	case map[string]any:
		return walkMap(t, n, set, blocked)
	case []any:
		return walkSlice(t, n, set, blocked)
	case string:
		if n != nil && n.Action() != nil && !blocked[n] {
			return Transform(t, *n.Action())
		}
		return matchValues(t, set)
	default:
		return v
	}
}

func matchValues(s string, set *rule.Set) string {
	for _, vr := range set.ValueRules() {
		if vr.Regexp().MatchString(s) {
			return Transform(s, vr.Action())
		}
	}
	return s
}

// Transform 执行单一脱敏动作。
func Transform(s string, a rule.Action) string {
	switch a.Kind {
	case rule.Replace:
		return a.Replacement
	case rule.Hash:
		sum := sha256.Sum256([]byte(s))
		return "sha256:" + hex.EncodeToString(sum[:])
	case rule.Truncate:
		r := []rune(s)
		if len(r) <= a.N {
			return s
		}
		return string(r[:a.N]) + truncSuffix
	}
	return s
}
