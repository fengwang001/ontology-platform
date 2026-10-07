package ontology

// Lease 表示一次成功获得的独占占用权。占用期内的全部修改都暂存在
// Lease 上，Commit 时与释放作为同一个原子事件对外发布：任何外部读取
// 要么看到占用开始之前的完整状态，要么看到占用结束之后的完整状态。
type Lease struct {
	store     *Store
	actionID  string
	keys      []string
	token     int64
	expiresAt int64
	ttl       int64
	staged    map[string]stagedState
	released  bool
}

type stagedState struct {
	state string
	props Props
	links map[string][]string
}

// ActionID 返回持有方动作标识。
func (l *Lease) ActionID() string { return l.actionID }

// Keys 返回本次占用覆盖的全部实例键（全局序）。
func (l *Lease) Keys() []string { return append([]string(nil), l.keys...) }

// Token 返回占用权围栏令牌。
func (l *Lease) Token() int64 { return l.token }

// ExpiresAt 返回租约到期时刻（Clock 毫秒读数）。
func (l *Lease) ExpiresAt() int64 { return l.expiresAt }

// Stage 暂存对某实例的状态转换与属性/链接修改，占用期间不对外可见。
// props/links 为 nil 的部分保持不变。
func (l *Lease) Stage(key, state string, props Patch, links map[string][]string) Outcome {
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if out := l.validateLocked(now); out != OutcomeCommitted {
		return out
	}
	st, ok := l.staged[key]
	if !ok {
		s.recordLocked(Decision{Kind: "stage", At: now, Caller: l.actionID,
			Keys: []string{key}, Token: l.token, Outcome: OutcomeInvalidLease,
			Reason: "key not covered by lease"})
		return OutcomeInvalidLease
	}
	if state != "" {
		st.state = state
	}
	if props != nil {
		if st.props == nil {
			st.props = Props{}
		}
		for k, v := range props {
			st.props[k] = v
		}
	}
	if links != nil {
		st.links = cloneLinks(links)
	}
	l.staged[key] = st
	s.recordLocked(Decision{Kind: "stage", At: now, Caller: l.actionID,
		Keys: []string{key}, Token: l.token, State: st.state,
		Patch: cloneProps(st.props), Links: cloneLinks(st.links),
		Outcome: OutcomeCommitted})
	return OutcomeCommitted
}

// Heartbeat 在占用仍有效时延长租约；持有方进程仍存活即应周期续约。
func (l *Lease) Heartbeat() Outcome {
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if out := l.validateLocked(now); out != OutcomeCommitted {
		return out
	}
	l.expiresAt = now + l.ttl
	for _, key := range l.keys {
		s.instances[key].expiresAt = l.expiresAt
	}
	s.recordLocked(Decision{Kind: "heartbeat", At: now, Caller: l.actionID, Keys: l.keys,
		Token: l.token, Outcome: OutcomeCommitted})
	return OutcomeCommitted
}

// Commit 原子地发布全部暂存修改、推进各实例版本并释放占用权。
// “发布属性变更”与“释放占用”是同一临界区内的同一个事件。
func (l *Lease) Commit() Outcome {
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if out := l.validateLocked(now); out != OutcomeCommitted {
		return out
	}
	for _, key := range l.keys {
		r := s.instances[key]
		st := l.staged[key]
		r.state = st.state
		r.props = cloneProps(st.props)
		r.links = cloneLinks(st.links)
		r.version++
		r.holder = ""
		r.expiresAt = 0
	}
	l.released = true
	s.recordLocked(Decision{Kind: "commit", At: now, Caller: l.actionID, Keys: l.keys,
		Token: l.token, Outcome: OutcomeCommitted})
	return OutcomeCommitted
}

// Release 不发布任何暂存修改，直接释放占用权。
func (l *Lease) Release() Outcome {
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if out := l.validateLocked(now); out != OutcomeCommitted {
		return out
	}
	for _, key := range l.keys {
		r := s.instances[key]
		r.holder = ""
		r.expiresAt = 0
	}
	l.released = true
	s.recordLocked(Decision{Kind: "release", At: now, Caller: l.actionID, Keys: l.keys,
		Token: l.token, Outcome: OutcomeCommitted})
	return OutcomeCommitted
}

// validateLocked 是占用是否仍归本 Lease 所有的唯一校验：持有者、
// 围栏令牌与租约时刻必须同时吻合，且租约尚未到期。
func (l *Lease) validateLocked(now int64) Outcome {
	if l.released {
		return OutcomeInvalidLease
	}
	for _, key := range l.keys {
		r, ok := l.store.instances[key]
		if !ok || r.holder != l.actionID || r.expiresAt != l.expiresAt || r.expiresAt <= now {
			return OutcomeInvalidLease
		}
	}
	return OutcomeCommitted
}
