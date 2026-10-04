package edit

import "ontology/cue"

// Result 是重定时一条字幕轨的产物。
type Result struct {
	Cues    []cue.Cue
	Splits  int
	Dropped int
}

// Retime 按剪辑表重定时全部字幕。
//
// 先在严格位于字幕内部的插入点 (s < at < e) 处把字幕切成若干同文本片；
// 每片 [p,q) 映射为 [fR(p), fL(q))；删除段从片中挖走时长但不切分。
// 新区间时长小于 dmin 的片丢弃（含零长）。结果按时间升序且互不重叠。
func Retime(c *Compiled, cues []cue.Cue, dmin int64) Result {
	var res Result
	k := len(c.delA) + len(c.insAt)
	limit := int64(probeLimit(k))
	enumLimit := int64(2 * ceilLog2(k+2))
	for _, cu := range cues {
		// 枚举内部插入点：一次下界二分定位，再顺序扫描。
		c.probes = 0
		first := c.upperBound(c.insAt, cu.Start) // 第一个 at > start
		last := c.lowerBound(c.insAt, cu.End)    // 第一个 at >= end
		enumProbes := c.probes
		cuts := c.insAt[first:last]
		res.Splits += len(cuts)

		// 枚举触碰数 ≤ 内部插入点数 + 2*ceil(log2(k+2))。
		if enumProbes > enumLimit {
			panic("edit: cut enumeration probe budget exceeded")
		}

		emit := func(p, q int64) {
			c.probes = 0 // 每个片独立核算
			np := c.FR(p)
			nq := c.FL(q)
			used := c.probes
			c.probes = 0
			// 每片两端映射比较数 ≤ 4*ceil(log2(k+2))。
			if used > limit {
				panic("edit: retime probe budget exceeded")
			}
			if nq-np >= dmin {
				res.Cues = append(res.Cues, cue.Cue{Start: np, End: nq, Text: cu.Text})
			} else {
				res.Dropped++
			}
		}
		prev := cu.Start
		for _, at := range cuts {
			emit(prev, at)
			prev = at
		}
		emit(prev, cu.End)
	}
	return res
}
