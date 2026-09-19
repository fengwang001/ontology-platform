package scope

// OnDone 注册一个收尾钩子。
//
// 作用域结束时钩子按注册顺序的逆序（LIFO）执行，每个恰好执行一次；
// 若注册时作用域已结束，则立即同步调用该钩子一次。
// 单个钩子 panic 不影响其余钩子执行，也不会使进程崩溃。
func (s *Scope) OnDone(f func()) {
	if f == nil {
		return
	}

	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		s.runOneHook(f)
		return
	}
	s.hooks = append(s.hooks, f)
	s.mu.Unlock()
}

// runHooks 在结束路径上执行全部已注册钩子（LIFO，各一次）。
// 调用时终态已落定，后续 OnDone 不会再把钩子追加进列表。
func (s *Scope) runHooks() {
	s.mu.Lock()
	hooks := s.hooks
	s.hooks = nil
	s.mu.Unlock()

	for i := len(hooks) - 1; i >= 0; i-- {
		s.runOneHook(hooks[i])
	}
}

// runOneHook 执行单个钩子并吞掉其 panic，保证收尾流程不被打断。
func (s *Scope) runOneHook(f func()) {
	defer func() { _ = recover() }()
	f()
}
