// Package span 记录原文区间与输出区间的双向偏移映射。
// 只在偏离恒等处存事件（删除区间或插入点），区间数只随删除点数增长。
package span

// Map 是一段规范化过程累积出的不可变映射。零值是恒等映射。
type Map struct {
	items []item // 按 orig 有序
	// lastChecks 是最近一次查询二分检查的事件区间数（非导出计数器）。
	lastChecks int
}

type item struct {
	orig    int // 删除区间起点，或插入点原文坐标
	out     int // 该事件左侧在输出中的坐标
	del     int // 删除长度（插入为 0）
	ins     int // 插入长度（删除为 0）
	origEnd int // 事件右边界原文坐标（插入点等于 orig）
	cd, ci  int // 截至本事件（含）的累计删除 / 插入量
}

// Builder 按时间顺序接收删除与插入，最后生成 Map。
type Builder struct{ items []item }

// Delete 记录原文 [start,end) 整段被删除，其输出锚点为 outPos。
func (b *Builder) Delete(start, end, outPos int) {
	if end <= start {
		return
	}
	if n := len(b.items); n > 0 && b.items[n-1].origEnd == start && b.items[n-1].del > 0 {
		b.items[n-1].origEnd = end
		b.items[n-1].del = end - b.items[n-1].orig
		return
	}
	b.items = append(b.items, item{orig: start, origEnd: end, out: outPos, del: end - start})
}

// Insert 记录在原文坐标 atOrig 处插入 insLen 个输出字节（输出起点 outPos）。
func (b *Builder) Insert(atOrig, outPos, insLen int) {
	if insLen <= 0 {
		return
	}
	b.items = append(b.items, item{orig: atOrig, origEnd: atOrig, out: outPos, ins: insLen})
}

// Build 冻结为 Map。
func (b *Builder) Build() Map {
	var cd, ci int
	for i := range b.items {
		cd += b.items[i].del
		ci += b.items[i].ins
		b.items[i].cd, b.items[i].ci = cd, ci
	}
	m := Map{items: b.items}
	b.items = nil
	return m
}

// 第一个 origEnd > i 的事件（删除区间内部由此命中）。
func (m Map) findOrig(i int) int {
	lo, hi, c := 0, len(m.items), 0
	for lo < hi {
		c++
		mid := (lo + hi) / 2
		if m.items[mid].origEnd <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = c
	return lo
}

// 第一个 out+ins > o 的事件（插入区间内部由此命中）。
func (m Map) findOut(o int) int {
	lo, hi, c := 0, len(m.items), 0
	for lo < hi {
		c++
		mid := (lo + hi) / 2
		if m.items[mid].out+m.items[mid].ins <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = c
	return lo
}

// ToOut 返回原文偏移 i 对应的输出偏移：原文前缀 [0,i) 的输出长度。
func (m Map) ToOut(i int) int {
	k := m.findOrig(i)
	if k > 0 {
		p := m.items[k-1]
		if p.del > 0 && i >= p.orig && i < p.origEnd {
			return p.out // 删除区间内部：锚定其输出位置（换行之前）
		}
	}
	if k == 0 {
		return i
	}
	var cd, ci int
	if k > 0 {
		cd, ci = m.items[k-1].cd, m.items[k-1].ci
	}
	return i - cd + ci
}

// ToOrig 返回输出偏移 o 对应的原文偏移。
func (m Map) ToOrig(o int) int {
	k := m.findOut(o)
	var cd, ci int
	if k > 0 {
		cd, ci = m.items[k-1].cd, m.items[k-1].ci
	}
	if k < len(m.items) {
		p := m.items[k]
		if p.ins > 0 && o >= p.out {
			return p.orig // 合成插入字节（含尾界）：钳制到插入点
		}
	}
	return o - ci + cd
}

// LastChecks 返回最近一次 ToOrig/ToOut 查询二分检查的区间数。
func (m Map) LastChecks() int { return m.lastChecks }

// Len 返回映射中的事件区间数。
func (m Map) Len() int { return len(m.items) }

// Shift 返回把全部原文/输出坐标平移后的映射（用于分段坐标平移）。
func (m Map) Shift(origOff, outOff int) Map {
	cp := make([]item, len(m.items))
	for i, it := range m.items {
		it.orig += origOff
		it.origEnd += origOff
		it.out += outOff
		cp[i] = it
	}
	return Map{items: cp}
}

// Merge 把多段已平移、按原文有序的映射合并为一个（重复事件按序共存，二分仍成立）。
func Merge(parts ...Map) Map {
	n := 0
	for _, p := range parts {
		n += len(p.items)
	}
	all := make([]item, 0, n)
	for _, p := range parts {
		all = append(all, p.items...)
	}
	return Map{items: all}
}
