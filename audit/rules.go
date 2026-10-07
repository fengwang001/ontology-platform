package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Effect 是一条规则的效果。
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Rule 是一条权限规则：当主体、目标、动作均匹配时生效。
// 匹配串支持精确匹配与 "*" 通配。
type Rule struct {
	Effect   Effect   `json:"effect"`
	Subjects []string `json:"subjects"`
	Targets  []string `json:"targets"`
	Actions  []string `json:"actions"`
}

// RuleSet 是一个权限规则版本的完整内容。
// 评估语义：按规则顺序首个匹配者生效；无匹配时默认拒绝。
type RuleSet struct {
	Rules []Rule `json:"rules"`
}

// Evaluate 是确定性纯函数：仅由规则内容与输入决定结果。
// 语义：按规则声明顺序取首个主体、目标、动作均匹配的规则，
// 其效果即为结果；无任何匹配时默认拒绝。
func Evaluate(rs RuleSet, subject, target string, req Request) Decision {
	for _, r := range rs.Rules {
		if matchAny(r.Subjects, subject) && matchAny(r.Targets, target) && matchAny(r.Actions, req.Action) {
			return r.Effect == EffectAllow
		}
	}
	return DecisionDeny
}

func matchAny(patterns []string, value string) bool {
	for _, p := range patterns {
		if p == "*" || p == value {
			return true
		}
	}
	return false
}

// Canonical 返回规则内容的规范序列化形式（用于哈希）。
// 结构体字段顺序固定、map 键由 encoding/json 排序，输出确定。
func Canonical(rs RuleSet) []byte {
	b, err := json.Marshal(rs)
	if err != nil {
		panic(err) // RuleSet 仅含可序列化字段，不会失败
	}
	return b
}

// ContentHash 返回规则内容的哈希标识。
func ContentHash(rs RuleSet) string {
	sum := sha256.Sum256(Canonical(rs))
	return hex.EncodeToString(sum[:])
}
