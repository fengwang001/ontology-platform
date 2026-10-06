// Package forward 计算组播转发端口集合。
//
// 本地链路组（0xE0000000-0xE00000FF）泛洪；有未到期成员的组转发到
// 成员端口与路由器端口的并集；无成员的组按 floodUnknown 决定泛洪或
// 仅路由器端口。计算是纯函数，入端口一律排除。
package forward

// IsMulticast 报告 group 是否落在 0xE0000000-0xEFFFFFFF。
func IsMulticast(group uint32) bool {
	return group&0xF0000000 == 0xE0000000
}

// IsLinkLocal 报告 group 是否为本地链路组 0xE0000000-0xE00000FF。
func IsLinkLocal(group uint32) bool {
	return group >= 0xE0000000 && group <= 0xE00000FF
}

// Forwarder 持有转发策略配置，无内部状态。
type Forwarder struct {
	ports        int
	floodUnknown bool
}

// New 构造转发器，ports 为端口总数。
func New(ports int, floodUnknown bool) *Forwarder {
	return &Forwarder{ports: ports, floodUnknown: floodUnknown}
}

// Ports 返回出端口升序集合。members 与 routers 须为升序且不含 inPort
// 之外的约束，本函数负责去重并去掉 inPort。
func (f *Forwarder) Ports(group uint32, inPort int, members, routers []int) []int {
	switch {
	case IsLinkLocal(group):
		return f.flood(inPort)
	case len(members) > 0:
		return unionExclude(members, routers, inPort)
	case f.floodUnknown:
		return f.flood(inPort)
	default:
		return exclude(routers, inPort)
	}
}

func (f *Forwarder) flood(inPort int) []int {
	out := make([]int, 0, f.ports-1)
	for p := 1; p <= f.ports; p++ {
		if p != inPort {
			out = append(out, p)
		}
	}
	return out
}

func exclude(sorted []int, port int) []int {
	out := make([]int, 0, len(sorted))
	for _, p := range sorted {
		if p != port {
			out = append(out, p)
		}
	}
	return out
}

// unionExclude 归并两个升序切片，去重并去掉 port，结果升序。
func unionExclude(a, b []int, port int) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		var v int
		switch {
		case j >= len(b) || (i < len(a) && a[i] < b[j]):
			v = a[i]
			i++
		case i >= len(a) || b[j] < a[i]:
			v = b[j]
			j++
		default:
			v = a[i]
			i++
			j++
		}
		if v != port && (len(out) == 0 || out[len(out)-1] != v) {
			out = append(out, v)
		}
	}
	return out
}
