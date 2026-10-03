package activity

// validNow 校验全局时钟参数范围。
func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000_000 }

func validK(k int) bool { return k >= 1 && k <= 20 }

// prepare 完成带尝试号操作的公共前置：时钟→存在→到期推演→k→终局。
// 返回在副本上推演后的活动；任何拒绝都不改状态。
func (e *Executor) prepare(id []byte, k int, now int64) (*act, error) {
	if !validNow(now) || !validK(k) {
		return nil, ErrArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return nil, ErrClock
	}
	src, ok := e.acts[string(id)]
	if !ok {
		return nil, ErrNotFound
	}
	a := src.clone()
	e.probes = 0
	e.advance(a, now)
	if k != a.k {
		return nil, ErrStale
	}
	if a.state == Terminal {
		return nil, ErrTerminal
	}
	return a, nil
}

// prepareStart 用于不带 k 的 Start：到期推演后必须仍是 Scheduled。
func (e *Executor) prepareStart(id []byte, now int64) (*act, error) {
	if !validNow(now) {
		return nil, ErrArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return nil, ErrClock
	}
	src, ok := e.acts[string(id)]
	if !ok {
		return nil, ErrNotFound
	}
	a := src.clone()
	e.probes = 0
	e.advance(a, now)
	if a.state == Terminal {
		return nil, ErrTerminal
	}
	if a.state != Scheduled {
		return nil, ErrState
	}
	return a, nil
}

func (e *Executor) commit(a *act, now int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.acts[string(a.id)] = a
	e.clock = now
}
