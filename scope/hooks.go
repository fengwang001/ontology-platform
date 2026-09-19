package scope

// OnDone 注册收尾钩子。钩子按注册逆序（LIFO）执行，每个恰好一次；
// 在已结束的作用域上注册时立即执行一次。钩子 panic 不影响其余钩子。
func (s *Scope) OnDone(f func()) {
	s.mu.Lock()
	if !s.closed {
		s.hooks = append(s.hooks, f)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	runHook(f)
}

// runHooks 按 LIFO 顺序运行钩子。
func runHooks(hooks []func()) {
	for i := len(hooks) - 1; i >= 0; i-- {
		runHook(hooks[i])
	}
}

// runHook 运行单个钩子并吞掉 panic。
func runHook(f func()) {
	defer func() { _ = recover() }()
	f()
}
