package scope

import "time"

// NewRoot 创建一棵作用域树的根。
// now 为注入时钟；所有截止判定只通过 Tick 按该时钟完成。
func NewRoot(now func() time.Time, deadline time.Time) *Scope {
	return &Scope{
		now:      now,
		deadline: deadline,
		children: make(map[*Scope]struct{}),
		done:     make(chan struct{}),
		origin:   nil,
	}
}

// Child 在当前作用域下派生一个子作用域。
// deadline 为零值表示继承父作用域的实际截止时间；
// 非零值时实际截止时间取父子中更早者（截止时间只能收紧）。
func (s *Scope) Child(deadline time.Time) *Scope {
	effective := s.deadline
	if !deadline.IsZero() && (effective.IsZero() || deadline.Before(effective)) {
		effective = deadline
	}

	child := &Scope{
		now:      s.now,
		parent:   s,
		deadline: effective,
		children: make(map[*Scope]struct{}),
		done:     make(chan struct{}),
	}

	s.mu.Lock()
	ended := s.ended
	if !ended {
		s.children[child] = struct{}{}
	}
	s.mu.Unlock()

	// 父作用域已结束：子作用域必须立刻以连坐方式结束，
	// 但仍作为正常节点存在，其收尾钩子照常执行。
	if ended {
		s.mu.Lock()
		origin := s.origin
		s.mu.Unlock()
		child.finishFromAncestor(origin)
	}
	return child
}

// finish 以“自身触发”的方式结束作用域。重复调用不会改写首次结果。
func (s *Scope) finish(err error, reason Reason) {
	if !s.markEnded(err, reason, s) {
		return
	}
	close(s.done)
	s.propagate(s)
	s.runHooks()
}

// finishFromAncestor 以“祖先触发”的方式连坐结束作用域。
func (s *Scope) finishFromAncestor(origin *Scope) {
	if !s.markEnded(ErrAncestorEnded, origin.reason, origin) {
		return
	}
	close(s.done)
	s.propagate(origin)
	s.runHooks()
}

// propagate 把结束事件传播给所有后代。调用时调用者自身已完成终态落定，
// 此处不得持有 s.mu，以免与子节点结束路径形成锁序环。
func (s *Scope) propagate(origin *Scope) {
	s.mu.Lock()
	kids := make([]*Scope, 0, len(s.children))
	for kid := range s.children {
		kids = append(kids, kid)
	}
	s.mu.Unlock()

	for _, kid := range kids {
		kid.finishFromAncestor(origin)
	}
}
