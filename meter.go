package billing

// 读数序列：插入、翻转判定、估抄条件、换表分段。

// insertActual 插入一条实抄读数（位置 idx 为插入点），并重算受影响的 pos。
// 调用方已完成全部校验（乱序/非法/估抄替代等）。
func (m *Meter) insertActual(idx int, r *Reading) {
	m.readings = append(m.readings, nil)
	copy(m.readings[idx+1:], m.readings[idx:])
	m.readings[idx] = r
	m.recomputePositions(idx)
}

// appendEstimate 追加一条估抄读数（必定在序列末尾）。
func (m *Meter) appendEstimate(r *Reading) {
	m.readings = append(m.readings, r)
	m.recomputePositions(len(m.readings) - 2)
}

// startNewSegment 在 now 时刻换表：旧表终读数 oldFinal，新表量程 newCap、起始 newStart。
func (m *Meter) startNewSegment(now, oldFinal, newCap, newStart int64) {
	if len(m.readings) == 0 || m.readings[len(m.readings)-1].Time < now {
		oldEnd := &Reading{Time: now, Value: oldFinal, segStart: false, cap: m.cap}
		m.readings = append(m.readings, oldEnd)
	}
	newStartR := &Reading{Time: now, Value: newStart, segStart: true, cap: newCap}
	m.readings = append(m.readings, newStartR)
	m.cap = newCap
	m.recomputePositions(0)
}

// recomputePositions 从锚点下标 idx 开始重算 pos。idx 若不是段首/首元素，
// 其 pos 由前一锚点（同段）推导；换表当刻新表从旧表累计位置接续、当刻用量为 0。
func (m *Meter) recomputePositions(idx int) {
	rs := m.readings
	if idx < 0 {
		idx = 0
	}
	if idx == 0 || rs[idx].segStart {
		rs[idx].pos = 0
	} else {
		rs[idx].pos = rs[idx-1].pos + rolloverDelta(rs[idx-1].Value, rs[idx].Value, rs[idx-1].cap)
	}
	for j := idx; j+1 < len(rs); j++ {
		cur, next := rs[j], rs[j+1]
		if next.segStart {
			// 换表当刻：新表从起始读数起算，接续旧表累计位置，当刻用量为 0。
			next.pos = cur.pos
			continue
		}
		next.pos = cur.pos + rolloverDelta(cur.Value, next.Value, cur.cap)
	}
}

// rolloverDelta 判定相邻读数间用量：后值>=前值为正常；
// 变小且差额恰在量程一半以内（含一半）视为翻转一次；超过一半视为非法（返回 ok=false）。
func rolloverDelta(prev, next, cap int64) (delta int64) {
	if next >= prev {
		return next - prev
	}
	diff := prev - next
	if diff <= cap/2 {
		return cap + next - prev
	}
	return -1 // 非法，由调用方经 legalReading 预先拦截
}

// legalReading 校验相对上一实抄/锚点读数的合法性；illegal 表示翻转差额超半。
func legalReading(prev, next, cap int64) (illegal bool) {
	if next >= prev {
		return false
	}
	return prev-next > cap/2
}

// checkEstimate 校验估抄条件：
// 序列必须已有读数且上一条为锚点；时刻严格递增（否则乱序）；
// 距上一次实抄严格超过 G；估抄值不得小于上一条读数。
func (b *Building) checkEstimate(m *Meter, in ReadingInput) error {
	rs := m.readings
	if len(rs) == 0 {
		return errf(ErrEstimateNotAllowed, "no prior actual reading")
	}
	last := rs[len(rs)-1]
	if in.Time <= last.Time {
		return errf(ErrReadingOutOfOrder, "estimate time %d <= last reading %d", in.Time, last.Time)
	}
	var lastActual *Reading
	for i := len(rs) - 1; i >= 0; i-- {
		if !rs[i].Estimated {
			lastActual = rs[i]
			break
		}
	}
	if lastActual == nil {
		return errf(ErrEstimateNotAllowed, "no prior actual reading")
	}
	if in.Time-lastActual.Time <= b.estGap {
		return errf(ErrEstimateNotAllowed,
			"distance %d from last actual not greater than G=%d", in.Time-lastActual.Time, b.estGap)
	}
	if in.Value < last.Value {
		return errf(ErrEstimateNotAllowed, "estimated value %d < last reading %d", in.Value, last.Value)
	}
	return nil
}

// checkIllegalAround 校验插入点两侧翻转差额不得超过量程一半。
// idx 为第一条 Time>=time 的读数下标（且该处不存在同时刻读数）。
func checkIllegalAround(m *Meter, time, value int64, idx int) error {
	rs := m.readings
	if idx > 0 {
		prev := rs[idx-1]
		// idx-1 可能是某段旧表终读数；其 cap 即旧表量程。
		if legalReading(prev.Value, value, prev.cap) {
			return errf(ErrReadingIllegal,
				"drop %d from %d exceeds half of capacity %d", prev.Value-value, prev.Value, prev.cap)
		}
	}
	if idx < len(rs) {
		next := rs[idx]
		// idx 指向新段起点（换表当刻）时，同段的右锚点是它前一条旧表终读数。
		if next.segStart {
			next = rs[idx-1]
		}
		if legalReading(value, next.Value, next.cap) {
			return errf(ErrReadingIllegal,
				"drop to next reading %d exceeds half of capacity %d", next.Value, next.cap)
		}
	}
	return nil
}

// clone 深拷贝一只表，用于“先试算后提交”，被拒绝时原表毫发无损。
func (m *Meter) clone() *Meter {
	cp := &Meter{id: m.id, cap: m.cap, readings: make([]*Reading, len(m.readings))}
	for i, r := range m.readings {
		rc := *r
		cp.readings[i] = &rc
	}
	return cp
}
