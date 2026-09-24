// Package span 记录原文区间与输出区间的对应关系，并支持双向偏移映射。
// 映射只随“删除点”增长：纯拷贝/纯删除连续段会被合并。
package span

// Run 表示一段从 OrigStart 开始、长 OrigLen 的原文，
// 被映射到从 OutStart 开始、长 OutLen 的输出。OutLen==0 表示该段被删除。
// 多个相邻 Run 的 OrigStart/OutStart 单调不减；被删 Run 与下一 Run
// 可能共享起点，这正是被删字节 ToOut 取下一保留偏移的编码方式。
type Run struct {
	OrigStart, OrigLen int
	OutStart, OutLen   int
}

func (r Run) origEnd() int { return r.OrigStart + r.OrigLen }
func (r Run) outEnd() int  { return r.OutStart + r.OutLen }

// Builder 累积 Run 并提供二分查询。
type Builder struct {
	runs    []Run
	origLen int
	outLen  int
	// probes 是最近一次 ToOrig/ToOut 二分检查过的区间数。
	probes int
}

// Add 追加一个 Run，相邻可合并（同斜率且端点相接）的 Run 自动合并。
func (b *Builder) Add(r Run) {
	b.origLen = max(b.origLen, r.origEnd())
	b.outLen = max(b.outLen, r.outEnd())
	if n := len(b.runs); n > 0 {
		p := &b.runs[n-1]
		// 拷贝段（长度都>0 且斜率同为 1）端点相接时合并；
		// 连续删除段（OutLen 都为 0 且 OutStart 相同）合并。
		copyJoin := p.OutLen > 0 && r.OutLen > 0 &&
			p.origEnd() == r.OrigStart && p.outEnd() == r.OutStart
		delJoin := p.OutLen == 0 && r.OutLen == 0 && p.OutStart == r.OutStart &&
			p.origEnd() == r.OrigStart
		if copyJoin || delJoin {
			p.OrigLen += r.OrigLen
			p.OutLen += r.OutLen
			return
		}
	}
	b.runs = append(b.runs, r)
}

// Runs 返回内部区间（只读使用）。
func (b *Builder) Runs() []Run { return b.runs }

// OrigLen / OutLen 返回已知两端总长。
func (b *Builder) OrigLen() int { return b.origLen }
func (b *Builder) OutLen() int  { return b.outLen }

// Probes 返回最近一次查询二分检查的区间数。
func (b *Builder) Probes() int { return b.probes }

// ToOrig 把输出偏移 o（∈[0,OutLen]）映射回原文偏移，单调不减。
func (b *Builder) ToOrig(o int) int {
	lo, hi := 0, len(b.runs)
	b.probes = 0
	for lo < hi {
		mid := (lo + hi) / 2
		b.probes++
		r := b.runs[mid]
		if o < r.OutStart {
			hi = mid
		} else if r.OutLen > 0 && o < r.outEnd() {
			return r.OrigStart + (o - r.OutStart)
		} else {
			lo = mid + 1
		}
	}
	if lo > 0 {
		return b.runs[lo-1].origEnd()
	}
	return 0
}

// ToOut 把原文偏移 i（∈[0,OrigLen]）映射到输出偏移，单调不减。
// 被删字节映射到删除点之后的下一个输出偏移（光标停在行尾换行符上）。
func (b *Builder) ToOut(i int) int {
	lo, hi := 0, len(b.runs)
	b.probes = 0
	for lo < hi {
		mid := (lo + hi) / 2
		b.probes++
		r := b.runs[mid]
		if i < r.OrigStart {
			hi = mid
		} else if i < r.origEnd() {
			if r.OutLen > 0 {
				return r.OutStart + (i - r.OrigStart)
			}
			return r.OutStart // 删除段：压向下一输出偏移
		} else {
			lo = mid + 1
		}
	}
	return b.outLen
}

// CutOutput 把输出截断到前 newOutLen 字节：超出部分的拷贝区间改为删除区间
// （映射到 newOutLen），并裁掉完全落在截断点之后的区间。供末尾策略同步映射。
func (b *Builder) CutOutput(newOutLen int) {
	if newOutLen >= b.outLen {
		return
	}
	kept := b.runs[:0]
	var origLen int
	for _, r := range b.runs {
		if r.OutStart >= newOutLen {
			break // 后续区间都在截断点之后
		}
		if r.OutLen > 0 && r.outEnd() > newOutLen {
			keep := newOutLen - r.OutStart
			origLen = r.OrigStart + keep
			kept = append(kept, Run{r.OrigStart, keep, r.OutStart, keep})
			break
		}
		kept = append(kept, r)
		origLen = r.origEnd()
	}
	b.runs = kept
	b.origLen, b.outLen = origLen, newOutLen
}
