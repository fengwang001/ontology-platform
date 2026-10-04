// Package policy 对给定 group 状态求值单台设备的有效配置。
//
// 逐键规则：在设备所属分组（含内置 *）中找出策略含该键者，
// 取 pr 最大；pr 并列取分组名字节序最小。胜者值为 Unset 时
// 该键不进入结果（遮蔽，不回落次高者）；否则取该字符串值（空串也是值）。
package policy

import "ontology/group"

// Effective 加读锁求值。dev 不存在时返回 nil。
func Effective(s *group.Store, dev group.Name) map[group.Name]string {
	s.RLock()
	defer s.RUnlock()
	return EffectiveLocked(s, dev)
}

// EffectiveLocked 在调用方已持锁时求值。
func EffectiveLocked(src group.PolicySource, dev group.Name) map[group.Name]string {
	type cand struct {
		pr    int
		gname group.Name
		val   group.Value
	}
	winners := map[group.Name]cand{}

	src.IterDeviceGroups(dev, func(gname group.Name, pr int, p group.Policy) {
		for k, v := range p {
			cur, ok := winners[k]
			if !ok || pr > cur.pr || (pr == cur.pr && gname < cur.gname) {
				winners[k] = cand{pr: pr, gname: gname, val: v}
			}
		}
	})

	if len(winners) == 0 {
		return map[group.Name]string{}
	}
	out := make(map[group.Name]string, len(winners))
	for k, c := range winners {
		if !c.val.Unset {
			out[k] = c.val.Val
		}
	}
	return out
}
