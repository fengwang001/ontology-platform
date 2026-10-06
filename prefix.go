package ontology

import "strings"

// familyWidth 返回地址族的字节长度与前缀位数上限；非法族返回 0,0。
func familyWidth(f AddrFamily) (bytes, maxBits int) {
	switch f {
	case FamilyV4:
		return 4, 32
	case FamilyV6:
		return 16, 128
	default:
		return 0, 0
	}
}

// maskPrefix 返回 addr 按 prefixLen 清零低位后的副本。
// 调用方须保证 prefixLen 已在族允许范围内；前缀长度之外的低位一律清零，不报错。
func maskPrefix(addr Addr, prefixLen int) Addr {
	out := make(Addr, len(addr))
	copy(out, addr)
	full := prefixLen / 8
	for i := full + 1; i < len(out); i++ {
		out[i] = 0
	}
	if rem := prefixLen % 8; rem != 0 && full < len(out) {
		out[full] &= ^byte(0xff >> rem)
	} else if full < len(out) {
		out[full] = 0
	}
	return out
}

// prefixCovers 判断 prefix 处长度为 prefixLen 的前缀是否覆盖 addr。
// prefixLen == 0 覆盖同族所有地址。要求三者长度一致且 prefix 已清零。
func prefixCovers(prefix Addr, prefixLen int, addr Addr) bool {
	full := prefixLen / 8
	for i := 0; i < full; i++ {
		if prefix[i] != addr[i] {
			return false
		}
	}
	if rem := prefixLen % 8; rem != 0 && full < len(prefix) {
		mask := byte(0xff << (8 - rem))
		if prefix[full]&mask != addr[full]&mask {
			return false
		}
	}
	return true
}

// normalizeName 规范化查询名字：先去掉恰好一个末尾点（若存在），再整体转小写。
// 只去掉一个点，因此 "a.." 与 "a." 不等价（前者规范化后仍是 "a."）。
func normalizeName(name string) string {
	if len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	return strings.ToLower(name)
}

// nameKey 是缓存中按(名字,类型)分桶的键。
type nameKey struct {
	name   string
	rrtype uint16
}

// prefixKey 唯一标识同(名字,类型,族,前缀,前缀长度)的缓存条目或在途合并组。
type prefixKey struct {
	name   nameKey
	family AddrFamily
	prefix string // maskPrefix 的结果以不可变字符串持有，可直接比较/做 map 键
	bits   int
}
