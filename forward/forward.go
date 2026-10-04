// Package forward 提供组播转发出口集合的无状态决策。
package forward

import "ontology/member"

// Decide 按当前已惰性到期后的成员端口与路由器端口计算转发出口集合。
//
//   - 本地链路组：泛洪到除 inPort 外的全部端口；
//   - 普通组且有未到期成员：成员端口 ∪ 路由器端口，再去掉 inPort；
//   - 普通组且无成员：floodUnknown 为真则泛洪，否则只走路由器端口，再去掉 inPort。
//
// 入端口取值 1..P；members、routers 均须升序。返回始终是新分配的升序切片。
func Decide(P int, group uint32, inPort int, members, routers []int, floodUnknown bool) []int {
	if member.IsLocalGroup(group) || (len(members) == 0 && floodUnknown) {
		out := make([]int, 0, P)
		for p := 1; p <= P; p++ {
			if p != inPort {
				out = append(out, p)
			}
		}
		return out
	}
	out := make([]int, 0, len(members)+len(routers))
	i, j := 0, 0
	for i < len(members) || j < len(routers) {
		var p int
		switch {
		case j == len(routers) || (i < len(members) && members[i] < routers[j]):
			p = members[i]
			i++
		case i == len(members) || (j < len(routers) && routers[j] < members[i]):
			p = routers[j]
			j++
		default: // 相等端口，取并集只保留一次。
			p = members[i]
			i++
			j++
		}
		if p != inPort {
			out = append(out, p)
		}
	}
	return out
}
