package activity

// 朴素逐毫秒参考模型：状态与规则完全按题目描述直接展开。
type refAct struct {
	state                 State
	k                     int
	t0, g, r, h, progress int64
	s2s, s2c, hb, sc      int64
	reason                Reason
	termAt                int64
}

func (a *refAct) fail(f int64, r Reason, m int, d0, cap int64) {
	if k := a.k; k >= m {
		a.state, a.reason, a.termAt = Terminal, r, f
		return
	}
	gp := f + backoff(d0, cap, a.k)
	if a.sc > 0 && gp >= a.t0+a.sc {
		a.state, a.reason, a.termAt = Terminal, r, f
		return
	}
	a.k++
	a.state, a.g = Waiting, gp
}

func backoff(d0, cap int64, k int) int64 {
	d := d0
	for i := 1; i < k; i++ {
		d *= 2
		if d > cap {
			d = cap
		}
	}
	if d > cap {
		return cap
	}
	return d
}

// tick 把副本从 clock 逐毫秒推进到 now，返回处理的到期事件数。
func (a *refAct) tick(clock, now int64, m int, d0, cap int64) int {
	events := 0
	for t := clock + 1; t <= now; t++ {
		switch a.state {
		case Terminal:
			return events
		case Waiting:
			if a.sc > 0 && t == a.t0+a.sc {
				a.state, a.reason, a.termAt = Terminal, ReasonSC, t
				events++
			} else if t == a.g {
				a.state = Scheduled
			}
		case Scheduled:
			if a.sc > 0 && t == a.t0+a.sc {
				a.state, a.reason, a.termAt = Terminal, ReasonSC, t
				events++
			} else if a.s2s > 0 && t == a.g+a.s2s {
				a.state, a.reason, a.termAt = Terminal, ReasonS2S, t
				events++
			}
		case Running:
			switch {
			case a.sc > 0 && t == a.t0+a.sc:
				a.state, a.reason, a.termAt = Terminal, ReasonSC, t
				events++
			case a.s2c > 0 && t == a.r+a.s2c:
				a.fail(t, ReasonS2C, m, d0, cap)
				events++
			case a.hb > 0 && t == a.h+a.hb:
				a.fail(t, ReasonHB, m, d0, cap)
				events++
			}
		}
	}
	return events
}

func (a *refAct) snapshot() Status {
	return Status{State: a.state, Attempt: a.k, Terminal: a.state == Terminal,
		Reason: a.reason, Time: a.termAt}
}

type refModel struct {
	a          *refAct
	clock      int64
	m          int
	d0, cap    int64
	exists     bool
	lastEvents int
}

func errEq(a, b error) bool {
	return (a == nil) == (b == nil) && (a == nil || errorIs(a, b) && errorIs(b, a))
}

func errorIs(err, target error) bool {
	type iser interface{ Is(error) bool }
	if x, ok := err.(iser); ok {
		return x.Is(target)
	}
	return err == target
}

// advance 提交到期推演（写操作使用：推演结果是操作判定的一部分且成功后保留）。
func (rm *refModel) advance(now int64) {
	rm.lastEvents = rm.a.tick(rm.clock, now, rm.m, rm.d0, rm.cap)
}

func (rm *refModel) start(now int64) (int, int64, error) {
	if now < 0 || now > 1e15 {
		return 0, 0, ErrArgument
	}
	if now < rm.clock {
		return 0, 0, ErrClock
	}
	saved := *rm.a
	rm.advance(now)
	if rm.a.state == Terminal {
		rm.a = &saved
		return 0, 0, ErrTerminal
	}
	if rm.a.state != Scheduled {
		rm.a = &saved
		return 0, 0, ErrState
	}
	rm.a.state, rm.a.r, rm.a.h = Running, now, now
	rm.clock = now
	return rm.a.k, rm.a.progress, nil
}

// kcall 模拟带尝试号操作的公共前置与 Running 状态要求。
func (rm *refModel) kcall(k int, now int64) error {
	if now < 0 || now > 1e15 || k < 1 || k > 20 {
		return ErrArgument
	}
	if now < rm.clock {
		return ErrClock
	}
	saved := *rm.a
	rm.advance(now)
	if k != rm.a.k {
		rm.a = &saved
		return ErrStale
	}
	if rm.a.state == Terminal {
		rm.a = &saved
		return ErrTerminal
	}
	if rm.a.state != Running {
		rm.a = &saved
		return ErrState
	}
	return nil
}

func (rm *refModel) commit(now int64) { rm.clock = now }

func rmStatus(rm *refModel, now int64) (Status, error) {
	if now < 0 || now > 1e15 {
		return Status{}, ErrArgument
	}
	if now < rm.clock {
		return Status{}, ErrClock
	}
	return rmStatusSnap(rm, now), nil
}

func rmStatusSnap(rm *refModel, now int64) Status {
	cp := *rm.a
	rm.lastEvents = cp.tick(rm.clock, now, rm.m, rm.d0, rm.cap)
	return cp.snapshot()
}
