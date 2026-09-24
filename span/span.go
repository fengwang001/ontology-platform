// Package span 记录原文区间与输出区间的对应关系，支持双向偏移查询。
// 区间类型只有三种：Keep（等长复制）、Del（原文删除，输出不增长）、
// Ins（输出追加，原文不增长，仅末尾策略追加换行时出现）。
package span

// Kind 是区间种类。
type Kind uint8

const (
	Keep Kind = iota
	Del
	Ins
)

// Seg 是一个原文/输出对应区间，坐标均相对全局。
type Seg struct {
	Kind     Kind
	OStart   int // 原文起点
	OE       int // 原文终点（Ins 时等于 OStart）
	OutStart int // 输出起点
	OutE     int // 输出终点（Del 时等于 OutStart）
}

// Map 是不可变查询视图，由 Builder.Build 产生。
type Map struct {
	segs      []Seg
	origLen   int
	outLen    int
	lastProbe int // 非导出：最近一次查询检查的区间数
}

// LastProbe 返回最近一次 ToOrig/ToOut 二分检查的区间数。
func (m *Map) LastProbe() int { return m.lastProbe }

// NumSegs 返回区间总数。
func (m *Map) NumSegs() int { return len(m.segs) }

func (m *Map) findOrig(i int) int {
	lo, hi, probes := 0, len(m.segs), 0
	for lo < hi {
		probes++
		mid := (lo + hi) / 2
		if m.segs[mid].OStart < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.segs) || m.segs[lo].OStart > i {
		lo--
	}
	m.lastProbe = probes
	return lo
}

func (m *Map) findOut(o int) int {
	lo, hi, probes := 0, len(m.segs), 0
	for lo < hi {
		probes++
		mid := (lo + hi) / 2
		if m.segs[mid].OutStart < o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.segs) || m.segs[lo].OutStart > o {
		lo--
	}
	m.lastProbe = probes
	return lo
}

// ToOut 把原文偏移 i（含终点 len）映射为输出偏移，单调不减。
func (m *Map) ToOut(i int) int {
	s := &m.segs[m.findOrig(i)]
	switch s.Kind {
	case Keep:
		return s.OutStart + (i - s.OStart)
	case Del:
		return s.OutStart
	default:
		if i == s.OStart {
			return s.OutStart
		}
		return s.OutE
	}
}

// ToOrig 把输出偏移 o（含终点 len）映射为原文偏移，单调不减。
func (m *Map) ToOrig(o int) int {
	s := &m.segs[m.findOut(o)]
	switch s.Kind {
	case Keep:
		return s.OStart + (o - s.OutStart)
	case Ins:
		return s.OStart
	default:
		return s.OStart
	}
}

// Builder 增量构造 Map；同类相邻区间自动合并。非并发安全。
type Builder struct {
	segs            []Seg
	origLen, outLen int
}

// Segs 返回已构造区间的拷贝（供 par 平移后重放）。
func (b *Builder) Segs() []Seg {
	out := make([]Seg, len(b.segs))
	copy(out, b.segs)
	return out
}

// FromSegs 用给定区间与长度直接构造 Builder（par 拼接用）。
func FromSegs(segs []Seg, origLen, outLen int) *Builder {
	return &Builder{segs: segs, origLen: origLen, outLen: outLen}
}

func (b *Builder) last() *Seg {
	if len(b.segs) == 0 {
		return nil
	}
	return &b.segs[len(b.segs)-1]
}

// Keep 追加 n 个等长复制字节。
func (b *Builder) Keep(n int) {
	if n <= 0 {
		return
	}
	if s := b.last(); s != nil && s.Kind == Keep {
		s.OE += n
		s.OutE += n
	} else {
		b.segs = append(b.segs, Seg{Keep, b.origLen, b.origLen + n, b.outLen, b.outLen + n})
	}
	b.origLen += n
	b.outLen += n
}

// Delete 追加 n 个被删原文字节。
func (b *Builder) Delete(n int) {
	if n <= 0 {
		return
	}
	if s := b.last(); s != nil && s.Kind == Del {
		s.OE += n
	} else {
		b.segs = append(b.segs, Seg{Del, b.origLen, b.origLen + n, b.outLen, b.outLen})
	}
	b.origLen += n
}

// Insert 追加 1 个无原文来源的输出字节（末尾策略补 \n）。
func (b *Builder) Insert() {
	b.segs = append(b.segs, Seg{Ins, b.origLen, b.origLen, b.outLen, b.outLen + 1})
	b.outLen++
}

// Retract 从输出尾部撤回 n 个字节（末尾策略收敛尾部空行），
// 把对应的 Keep 区间改记为 Del；n 不得超过现有输出长度。
func (b *Builder) Retract(n int) {
	var popped []Seg
	for need := n; need > 0; {
		s := b.segs[len(b.segs)-1]
		b.segs = b.segs[:len(b.segs)-1]
		popped = append(popped, s)
		need -= s.OutE - s.OutStart
	}
	rem := n
	for k := len(popped) - 1; k >= 0; k-- {
		s := popped[k]
		if s.Kind == Del || rem == 0 {
			b.segs = append(b.segs, s)
			continue
		}
		span := s.OutE - s.OutStart
		switch {
		case rem >= span:
			s.Kind = Del
			s.OutE = s.OutStart
			b.segs = append(b.segs, s)
			rem -= span
		default:
			b.segs = append(b.segs, Seg{Keep, s.OStart, s.OE - rem, s.OutStart, s.OutE - rem})
			b.segs = append(b.segs, Seg{Del, s.OE - rem, s.OE, s.OutE - rem, s.OutE - rem})
			rem = 0
		}
	}
	last := b.segs[len(b.segs)-1]
	b.origLen, b.outLen = last.OE, last.OutE
}

// Build 产出不可变查询视图。
func (b *Builder) Build() *Map {
	return &Map{segs: b.segs, origLen: b.origLen, outLen: b.outLen}
}

// OrigLen 返回已记录的原文长度。
func (b *Builder) OrigLen() int { return b.origLen }

// OutputLen 返回已记录的输出长度。
func (b *Builder) OutputLen() int { return b.outLen }
