// Package span 维护原文区间与输出区间的对应关系，支持左闭右开定义域
// （含终点 len）上的双向二分查询 ToOrig/ToOut，二者均单调不减。本包无外部依赖。
package span

// Entry：原文 [Orig,Orig+OrigLen) 对应输出 [Out,Out+OutLen)。
// OutLen=0 为删除；OrigLen=0 为纯插入（末尾策略合成换行）。
type Entry struct {
	Orig    int64
	Out     int64
	OrigLen int64
	OutLen  int64
}

// Map 是不可变映射。checked 为非导出计数器：最近一次查询检查的区间数。
type Map struct {
	e       []Entry // 按 Orig 递增的全部区间
	k       []Entry // 按 Out 递增的 kept 区间（OutLen>0）
	checked int
}

// Builder 增量收集区间并构建 Map。
type Builder struct{ e []Entry }

// Append 追加区间；与上一同形区间相邻时合并，保证无删除文本只有一个区间。
func (b *Builder) Append(x Entry) {
	if n := len(b.e); n > 0 {
		p := &b.e[n-1]
		same := (p.OrigLen == 0) == (x.OrigLen == 0) && (p.OutLen == 0) == (x.OutLen == 0)
		if same && p.Orig+p.OrigLen == x.Orig && p.Out+p.OutLen == x.Out {
			p.OrigLen += x.OrigLen
			p.OutLen += x.OutLen
			return
		}
	}
	b.e = append(b.e, x)
}

// Build 生成不可变 Map。
func (b *Builder) Build() *Map {
	k := make([]Entry, 0, len(b.e))
	for _, x := range b.e {
		if x.OutLen > 0 {
			k = append(k, x)
		}
	}
	return &Map{e: b.e, k: k}
}

// Entries 返回区间副本（供 par 坐标平移后重新拼接）。
func (b *Builder) Entries() []Entry {
	out := make([]Entry, len(b.e))
	copy(out, b.e)
	return out
}

// Cut 丢弃输出坐标 >= outEnd 的尾部（跨区间按 1:1 回缩），返回原文终点。
func (b *Builder) Cut(outEnd int64) int64 {
	for len(b.e) > 0 {
		x := &b.e[len(b.e)-1]
		if x.Out >= outEnd {
			b.e = b.e[:len(b.e)-1]
			continue
		}
		if x.Out+x.OutLen > outEnd {
			d := x.Out + x.OutLen - outEnd
			x.OutLen -= d
			x.OrigLen -= d
			if x.OrigLen == 0 && x.OutLen == 0 {
				b.e = b.e[:len(b.e)-1]
			}
		}
		break
	}
	if len(b.e) == 0 {
		return 0
	}
	x := b.e[len(b.e)-1]
	return x.Orig + x.OrigLen
}

func (m *Map) Entries() int { return len(m.e) }
func (m *Map) Checked() int { return m.checked }

// ToOrig 把输出偏移（含输出终点）映射回原文偏移，单调不减。
// 落在删除边界/插入点时取“其后第一个保留字节”；无则取原文长度。
func (m *Map) ToOrig(o int64) int64 {
	i, j := 0, len(m.k)
	m.checked = 0
	for i < j {
		h := int(uint(i+j) >> 1)
		m.checked++
		x := m.k[h]
		switch {
		case o < x.Out:
			j = h
		case o >= x.Out+x.OutLen:
			i = h + 1
		default:
			m.checked++
			return x.Orig + o - x.Out
		}
	}
	m.checked++
	if i < len(m.k) {
		return m.k[i].Orig
	}
	if len(m.e) > 0 {
		x := m.e[len(m.e)-1]
		return x.Orig + x.OrigLen
	}
	return 0
}

// ToOut 把原文偏移（含原文终点）映射到输出偏移，单调不减。
// 删除段内部偏移映射到其后边界（即下一个保留字节的输出位置）。
func (m *Map) ToOut(i int64) int64 {
	lo, hi := 0, len(m.e)
	m.checked = 0
	for lo < hi {
		h := int(uint(lo+hi) >> 1)
		m.checked++
		x := m.e[h]
		switch {
		case i < x.Orig:
			hi = h
		case i >= x.Orig+x.OrigLen:
			lo = h + 1
		default:
			m.checked++
			return x.Out + min(i-x.Orig, x.OutLen)
		}
	}
	m.checked++
	if lo < len(m.e) {
		return m.e[lo].Out
	}
	if len(m.e) > 0 {
		x := m.e[len(m.e)-1]
		return x.Out + x.OutLen
	}
	return 0
}
