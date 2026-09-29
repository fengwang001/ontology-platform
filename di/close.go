package di

// Close 等待在途解析结束后，逆序释放本作用域拥有的实例。幂等。
func (s *Scope) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// 等待所有在途解析结束：关闭后开始的解析在 enterResolve 处被拒。
	s.active.Wait()

	s.mu.Lock()
	owned := s.owned
	s.owned = nil
	s.cached = nil
	s.building = nil
	s.mu.Unlock()

	disposeOwnedReverse(owned)
	return nil
}

// Closed 报告作用域是否已关闭。
func (s *Scope) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close 先关闭全部子作用域，再逆序释放单例与归根瞬态。幂等。
func (c *Container) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	children := append([]*Scope(nil), c.root.childScopes...)
	c.mu.Unlock()

	// 先关闭全部子作用域（每个内部等待其在途解析结束）。
	for _, sc := range children {
		_ = sc.Close()
	}

	// 再关闭根作用域：等待归根的在途解析，逆序释放单例与归根瞬态。
	return c.root.Close()
}

// disposeOwnedReverse 逆序释放一个作用域拥有的节点。
// owned 按构造成功顺序追加，逆序本身保证“依赖者先于被依赖者”：
// 同一归属作用域内，依赖总是先于依赖者构造完成；
// 归属其它作用域的节点（如作用域实例依赖的单例）不在本列表中，
// 由其自己的作用域关闭负责，此处绝不递归释放。
func disposeOwnedReverse(owned []*node) {
	for i := len(owned) - 1; i >= 0; i-- {
		n := owned[i]
		if d, ok := n.value.(Disposer); ok {
			d.Dispose()
		}
	}
}
