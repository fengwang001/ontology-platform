package scope

// Cancel 以给定原因显式结束本作用域，并向所有后代传播。
// 结束是一次性的：首个原因不可改写。
func (s *Scope) Cancel(r Reason) {
	s.finish(ErrCanceled, r, s)
}

// Tick 按注入时钟重新判定截止时间（左闭右开：now >= deadline 即超时），
// 并递归判定整棵子树。
func (s *Scope) Tick() {
	s.mu.Lock()
	if !s.closed && !s.deadline.IsZero() && s.now().After(s.deadline) {
		children, hooks, _ := s.endLocked(ErrDeadlineExceeded, Reason("deadline exceeded"), s)
		s.mu.Unlock()
		s.afterEnd(children, hooks)
		return
	}
	children := make([]*Scope, 0, len(s.children))
	for c := range s.children {
		children = append(children, c)
	}
	s.mu.Unlock()
	for _, c := range children {
		c.Tick()
	}
}

// finish 结束本作用域；err 为 ErrCanceled/ErrDeadlineExceeded 时 origin 为自身，
// 传播时 err 为 ErrAncestorEnded 且 origin 指向原始触发者。
func (s *Scope) finish(err error, reason Reason, origin *Scope) {
	s.mu.Lock()
	children, hooks, ok := s.endLocked(err, reason, origin)
	s.mu.Unlock()
	if !ok {
		return
	}
	s.afterEnd(children, hooks)
}

// endLocked 在持锁状态下将作用域标记为结束，返回待传播的子节点与待运行的钩子。
func (s *Scope) endLocked(err error, reason Reason, origin *Scope) (children []*Scope, hooks []func(), ok bool) {
	if s.closed {
		return nil, nil, false
	}
	s.closed = true
	s.err = err
	s.reason = reason
	s.origin = origin
	close(s.done)
	children = make([]*Scope, 0, len(s.children))
	for c := range s.children {
		children = append(children, c)
	}
	s.children = make(map[*Scope]struct{})
	hooks = s.hooks
	s.hooks = nil
	return children, hooks, true
}

// afterEnd 在解锁后运行本作用域的钩子，并把结束传播给所有后代。
func (s *Scope) afterEnd(children []*Scope, hooks []func()) {
	runHooks(hooks)
	reason := s.Reason()
	for _, c := range children {
		c.finish(ErrAncestorEnded, reason, s)
	}
}
