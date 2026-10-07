package exports

import "sync/atomic"

// probeCounter 统计键选择过程中的 map 查找次数。
// 解析路径上每次哈希查找自增一次，测试据此验证
// 单次解析的开销只随请求长度增长、与表中键总数无关。
type probeCounter struct {
	n atomic.Int64
}

func (c *probeCounter) add() { c.n.Add(1) }

// lookupProbes 返回并复位计数器，仅供测试与基准使用。
func (t *ExportTable) lookupProbes() int64 {
	return t.probes.n.Swap(0)
}

// selectKey 在表中为请求子路径选择键。
// 返回选中的键、对应目标、通配符匹配段以及是否命中。
//
// 规则：精确键优先；否则在含星号的键中要求请求以星号前部分开头、
// 以星号后部分结尾且匹配段至少一个字符，满足者取星号前部分最长的，
// 仍并列则取整个键更长的。查找次数只与请求长度有关。
func (t *ExportTable) selectKey(request string) (key string, target Target, matched string, ok bool) {
	t.probes.add()
	if target, hit := t.exact[request]; hit {
		return request, target, "", true
	}
	n := len(request)
	// 星号前缀从长到短枚举：第一个产生匹配的前缀长度即最优。
	for plen := n; plen >= 0; plen-- {
		t.probes.add()
		group, hit := t.wildcards[request[:plen]]
		if !hit {
			continue
		}
		// 匹配段至少一个字符：plen+slen <= n-1。
		// 同前缀下星号后缀越长则整个键越长，从长到短枚举后缀。
		for slen := n - plen - 1; slen >= 0; slen-- {
			t.probes.add()
			entry, hit := group[request[n-slen:]]
			if hit {
				return entry.key, entry.target, request[plen : n-slen], true
			}
		}
	}
	return "", Target{}, "", false
}
