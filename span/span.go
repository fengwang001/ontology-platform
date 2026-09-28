// Package span 维护原文区间与输出区间的分段对应关系，支持双向偏移映射。
package span

// Seg 是一段连续映射：
//   - Typ=='='：identity，[A0,A1) 与 [O0,O1) 等长一一对应
//   - Typ=='-'：删除，原文 [A0,A1) 全部映射到单点 O0
//   - Typ=='+'：插入，单点 A0 对应输出 [O0,O1)
type Seg struct {
	A0, A1 int64
	O0, O1 int64
	Typ    byte
}

// Span 是按 (原文, 输出) 端点单调排列的段序列。
type Span struct {
	segs  []Seg
	checks int // 最近一次查询二分检查的段数（非导出计数器）
}

// Len 返回段数。
func (s *Span) Len() int { return len(s.segs) }

// Checks 返回最近一次 ToOrig/ToOut 查询检查的映射区间数。
func (s *Span) Checks() int { return s.checks }

// Append 追加一段，必要时与末段合并同类相邻段。
func (s *Span) Append(a0, a1, o0, o1 int64, typ byte) {
	if n := len(s.segs); n > 0 {
		last := &s.segs[n-1]
		if last.Typ == typ && last.A1 == a0 && last.O1 == o0 {
			last.A1, last.O1 = a1, o1
			return
		}
	}
	s.segs = append(s.segs, Seg{a0, a1, o0, o1, typ})
}

// ToOut 把原文偏移 i（0..=总长）映射为输出偏移。
func (s *Span) ToOut(i int64) int64 {
	lo, hi := 0, len(s.segs)
	for lo < hi {
		mid := (lo + hi) / 2
		s.checks++
		if s.segs[mid].A0 <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	k := lo - 1
	if k < 0 {
		return 0
	}
	sg := s.segs[k]
	switch sg.Typ {
	case '=':
		if i <= sg.A1 {
			return sg.O0 + (i - sg.A0)
		}
	case '-':
		return sg.O0
	case '+':
		return sg.O1
	}
	return sg.O1
}

// ToOrig 把输出偏移 o（0..=总长）映射回原文偏移。
func (s *Span) ToOrig(o int64) int64 {
	lo, hi := 0, len(s.segs)
	for lo < hi {
		mid := (lo + hi) / 2
		s.checks++
		if s.segs[mid].O0 <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	k := lo - 1
	if k < 0 {
		return 0
	}
	sg := s.segs[k]
	switch sg.Typ {
	case '=':
		if o <= sg.O1 {
			return sg.A0 + (o - sg.O0)
		}
	case '-':
		return sg.A1
	case '+':
		return sg.A0
	}
	return sg.A1
}

// Shift 把所有段的原文坐标加 da、输出坐标加 do（用于并行段平移）。
func (s *Span) Shift(da, do int64) {
	for i := range s.segs {
		s.segs[i].A0 += da
		s.segs[i].A1 += da
		s.segs[i].O0 += do
		s.segs[i].O1 += do
	}
}

// Sub 截取原文区间 [a0,a1) 对应的映射，结果为自包含 Span（内部坐标从 0 起）。
func (s *Span) Sub(a0, a1 int64) *Span {
	r := &Span{}
	for _, sg := range s.segs {
		if sg.A1 <= a0 {
			continue
		}
		if sg.A0 >= a1 {
			break
		}
		ca0, ca1 := sg.A0, sg.A1
		co0, co1 := sg.O0, sg.O1
		if ca0 < a0 {
			d := a0 - ca0
			ca0 = a0
			if sg.Typ == '=' {
				co0 += d
			}
		}
		if ca1 > a1 {
			d := ca1 - a1
			ca1 = a1
			if sg.Typ == '=' {
				co1 -= d
			}
		}
		if ca0 < ca1 || sg.Typ == '-' {
			r.Append(ca0-a0, ca1-a0, co0-s.O0, co1-s.O0, sg.Typ)
		}
	}
	return r
}

// Compose 先经 s 再经 t：t 的输入坐标对应 s 的输出坐标。
func Compose(s, t *Span) *Span {
	r := &Span{}
	for _, sg := range s.segs {
		switch sg.Typ {
		case '=':
			r.Append(sg.A0, sg.A1, t.ToOut(sg.O0), t.ToOut(sg.O1), '=')
		case '-':
			r.Append(sg.A0, sg.A1, t.ToOut(sg.O0), t.ToOut(sg.O0), '-')
		case '+':
			o := t.ToOut(sg.O0)
			r.Append(sg.A0, sg.A0, o, o+(sg.O1-sg.O0), '+')
		}
	}
	return r
}
