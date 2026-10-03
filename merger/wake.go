package merger

// Wake 在 w = max(e, last+g) 时刻执行一次唤醒：
// 候选为全部满足 n <= w 的定时器，按 (n+s, 编号字节序) 升序取前 B 个触发，
// 其余留下；被触发者记 late = max(0, w-(n+s))、k = floor((w-n)/P)，
// 随后 n 增加 (k+1)*P；last 置为 w。
func (m *Merger) Wake() (WakeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byEnd.len() == 0 {
		return WakeResult{}, ErrNoTimers
	}
	return m.wakeLocked(), nil
}

// wakeLocked 假定定时器非空且已持锁。
func (m *Merger) wakeLocked() WakeResult {
	w, _ := m.nextLocked()

	// 从以 n 为键的堆中逐个取出候选，考察次数 = 候选个数 + 1。
	var cand []*timer
	m.wakeExam = 0
	for {
		t := m.byStart.top()
		if t == nil {
			break
		}
		m.wakeExam++
		if t.n > w {
			break
		}
		m.byStart.pop()
		cand = append(cand, t)
	}
	sortCandidates(cand)

	fire := int(m.batch)
	if len(cand) < fire {
		fire = len(cand)
	}
	res := WakeResult{W: w, Left: len(cand) - fire}
	for _, t := range cand[:fire] {
		late := w - t.end()
		if late < 0 {
			late = 0
		}
		k := (w - t.n) / t.p
		t.n += (k + 1) * t.p
		res.Fired = append(res.Fired, Fired{ID: t.id, Late: late, K: k})
		m.byStart.push(t)
		m.byEnd.fix(t)
		m.stats.Fired++
		m.stats.Skipped += k
		if late > 0 {
			m.stats.Late++
		}
	}
	// 被留下的候选键值未变，重新放回以 n 为键的堆。
	for _, t := range cand[fire:] {
		m.byStart.push(t)
	}

	m.last = w
	m.hasLast = true
	m.stats.Wakes++
	return res
}

// AdvanceTo 在 Next() <= t 时重复 Wake()，单次调用至多 1e5 次，达上限即停。
// 返回途中全部 Wake 的结果；t 之前没有唤醒则时钟不动。
func (m *Merger) AdvanceTo(t int64) ([]WakeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t < 0 || t > maxT {
		return nil, ErrInvalidParam
	}
	if t < m.last {
		return nil, ErrClockBack
	}
	if m.byEnd.len() == 0 {
		return nil, ErrNoTimers
	}
	var out []WakeResult
	for i := 0; i < maxAdvanceWakes; i++ {
		w, _ := m.nextLocked()
		if w > t {
			break
		}
		out = append(out, m.wakeLocked())
	}
	return out, nil
}
