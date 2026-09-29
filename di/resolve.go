package di

import "fmt"

// buildState 是单例/作用域实例首次构造的单飞记录。
type buildState struct {
	done chan struct{}
	node *node
	err  error
}

// node 是一次解析中已成功构造（或暂存）的实例节点。
type node struct {
	name  string
	reg   *Registration
	value any

	// cacheScope：singleton -> root；scoped -> 入口作用域；transient -> nil。
	cacheScope *Scope
	// owner：实例挂在哪个作用域的 owned 列表上（决定由谁释放）。
	owner *Scope

	deps []*node

	// attempt：暂存作用域实例归属的解析尝试。
	attempt *attempt

	// committed：已被保留（单例成功即提交；作用域实例顶层成功后提交）。
	committed bool
	// ownedAttached：已挂到 owner.owned；回滚时摘除。
	ownedAttached bool
}

// attempt 是一次顶层 Resolve 的构造上下文。
type attempt struct {
	// 同一次解析内瞬态去重（菱形瞬态边共享同一实例）。
	transients map[string]*node
	// 同一次解析内作用域实例去重（含构造中的节点，避免菱形自锁）。
	scoped map[string]*node
	// 本次尝试新建节点（构造成功顺序），失败时逆序释放。
	created []*node
}

// Resolve 从根作用域解析服务。
func (c *Container) Resolve(name string) (any, error) {
	return c.root.Resolve(name)
}

// Resolve 从本作用域解析服务。
func (s *Scope) Resolve(name string) (any, error) {
	c := s.container
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrContainerClosed
	}
	if !c.frozen {
		c.mu.Unlock()
		return nil, ErrNotFrozen
	}
	reg := c.regs[name]
	c.mu.Unlock()
	if reg == nil {
		return nil, &RegistrationError{
			Kind:   "missing dependency",
			Detail: fmt.Sprintf("service %q is not registered", name),
		}
	}

	if !s.enterResolve() {
		if s.isRoot {
			return nil, ErrContainerClosed
		}
		return nil, ErrScopeClosed
	}
	defer s.active.Done()

	a := &attempt{
		transients: make(map[string]*node),
		scoped:     make(map[string]*node),
	}
	n, err := s.build(reg, s, a, nil)
	if err != nil {
		a.rollback()
		return nil, err
	}
	a.commit()
	return n.value, nil
}

// enterResolve 在作用域未关闭时登记一次在途解析。
func (s *Scope) enterResolve() bool {
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.active.Add(1)
	}
	s.mu.Unlock()
	return !closed
}

// build 构造或复用 reg 对应的实例。
// entry：顶层解析的入口作用域（作用域实例缓存于此）；parent：依赖图父节点。
func (s *Scope) build(reg *Registration, entry *Scope, a *attempt, parent *node) (*node, error) {
	switch reg.Lifetime {
	case Singleton:
		return s.buildCached(reg, entry, a, parent, entry.container.root)
	case Scoped:
		if entry.isRoot {
			return nil, ErrScopedFromRoot
		}
		// 同一尝试内（含构造中的节点）直接复用，避免菱形依赖自锁。
		if n, ok := a.scoped[reg.Name]; ok {
			return n, nil
		}
		n, err := s.buildCached(reg, entry, a, parent, entry)
		if err == nil && n.attempt == a {
			a.scoped[reg.Name] = n
		}
		return n, err
	case Transient:
		if n, ok := a.transients[reg.Name]; ok {
			return n, nil
		}
		return s.constructTransient(reg, entry, a, parent)
	default:
		return nil, fmt.Errorf("di: unknown lifetime for service %q", reg.Name)
	}
}

// buildCached 处理单例与作用域实例的缓存命中与并发单飞。
func (s *Scope) buildCached(reg *Registration, entry *Scope, a *attempt, parent *node, sc *Scope) (*node, error) {
	sc.mu.Lock()
	if v, ok := sc.cached[reg.Name]; ok {
		sc.mu.Unlock()
		return cachedNode(reg, sc, v), nil
	}
	if bs, ok := sc.building[reg.Name]; ok {
		sc.mu.Unlock()
		<-bs.done
		if bs.err != nil {
			return nil, bs.err
		}
		return bs.node, nil
	}
	bs := &buildState{done: make(chan struct{})}
	sc.building[reg.Name] = bs
	sc.mu.Unlock()

	n, err := s.constructFresh(reg, entry, a, parent, sc)
	if err != nil {
		sc.mu.Lock()
		bs.err = err
		delete(sc.building, reg.Name)
		sc.mu.Unlock()
		close(bs.done)
		return nil, err
	}

	if reg.Lifetime == Singleton {
		// 单例成功立即发布：缓存、唤醒同批等待者，闭包随单例保留。
		publishSingleton(n, bs)
	}
	// 作用域实例保持暂存：顶层成功 commit 发布，失败 rollback 通知等待者。
	return n, nil
}

// constructFresh 构造依赖并调用构造函数（调用方已取得单飞领导权）。
func (s *Scope) constructFresh(reg *Registration, entry *Scope, a *attempt, parent *node, sc *Scope) (*node, error) {
	owner := entry
	if parent != nil {
		owner = parent.owner
	}
	if reg.Lifetime == Singleton {
		owner = entry.container.root
	}
	n := &node{
		name:       reg.Name,
		reg:        reg,
		owner:      owner,
		cacheScope: sc,
	}
	if reg.Lifetime == Scoped {
		n.attempt = a
	}
	// 先登记到 created：构造中途依赖失败时，已成功的深层节点需要被逆序回滚。
	a.created = append(a.created, n)
	if err := constructInto(s, n, entry, a); err != nil {
		a.forgetCreated(n)
		return nil, err
	}
	// 构造成功后才挂到 owner.owned，保证 owned 顺序即构造完成顺序。
	attachOwned(n)
	return n, nil
}

// constructTransient 新建一个瞬态实例。
func (s *Scope) constructTransient(reg *Registration, entry *Scope, a *attempt, parent *node) (*node, error) {
	owner := entry
	if parent != nil {
		owner = parent.owner
	}
	n := &node{
		name:  reg.Name,
		reg:   reg,
		owner: owner,
	}
	a.transients[reg.Name] = n
	a.created = append(a.created, n)
	if err := constructInto(s, n, entry, a); err != nil {
		delete(a.transients, reg.Name)
		a.forgetCreated(n)
		return nil, err
	}
	attachOwned(n)
	return n, nil
}

// forgetCreated 从本次尝试的新建列表中摘除一个构造失败的节点。
func (a *attempt) forgetCreated(n *node) {
	for i := len(a.created) - 1; i >= 0; i-- {
		if a.created[i] == n {
			a.created = append(a.created[:i], a.created[i+1:]...)
			return
		}
	}
}

// constructInto 递归构造依赖并调用本节点构造函数。
func constructInto(s *Scope, n *node, entry *Scope, a *attempt) error {
	depValues := make(map[string]any, len(n.reg.Dependencies))
	for _, depName := range n.reg.Dependencies {
		depNode, err := s.build(entry.container.regs[depName], entry, a, n)
		if err != nil {
			return err
		}
		n.deps = append(n.deps, depNode)
		depValues[depName] = depNode.value
	}
	value, err := n.reg.Construct(depValues)
	if err != nil {
		return fmt.Errorf("di: construct %q failed: %w", n.name, err)
	}
	n.value = value
	return nil
}

// publishSingleton 发布成功单例：标记闭包已提交、写缓存并唤醒同批等待者。
func publishSingleton(n *node, bs *buildState) {
	markCommitted(n, map[*node]bool{})
	root := n.cacheScope
	root.mu.Lock()
	root.cached[n.name] = n.value
	bs.node = n
	delete(root.building, n.name)
	root.mu.Unlock()
	close(bs.done)
}

// markCommitted 将从 n 可达、尚未提交的本尝试节点标记为随单例保留。
// 遇到已提交节点（既有单例等）停止沿该边展开。
func markCommitted(n *node, seen map[*node]bool) {
	if seen[n] || n.committed {
		return
	}
	seen[n] = true
	n.committed = true
	for _, d := range n.deps {
		markCommitted(d, seen)
	}
}

// attachOwned 把节点挂到其归属作用域的 owned 列表末尾。
func attachOwned(n *node) {
	if n.owner == nil {
		return
	}
	n.ownedAttached = true
	n.owner.mu.Lock()
	n.owner.owned = append(n.owner.owned, n)
	n.owner.mu.Unlock()
}

func detachOwned(n *node) {
	if !n.ownedAttached || n.owner == nil {
		return
	}
	o := n.owner
	o.mu.Lock()
	for i := len(o.owned) - 1; i >= 0; i-- {
		if o.owned[i] == n {
			o.owned = append(o.owned[:i], o.owned[i+1:]...)
			break
		}
	}
	o.mu.Unlock()
}

func cachedNode(reg *Registration, sc *Scope, value any) *node {
	return &node{
		name:          reg.Name,
		reg:           reg,
		value:         value,
		owner:         sc,
		cacheScope:    sc,
		committed:     true,
		ownedAttached: true,
	}
}

// commit 在顶层解析成功时发布暂存的作用域实例并唤醒等待者。
func (a *attempt) commit() {
	for _, n := range a.created {
		if n.reg.Lifetime != Scoped || n.committed || n.attempt != a {
			continue
		}
		n.committed = true
		sc := n.cacheScope
		sc.mu.Lock()
		sc.cached[n.name] = n.value
		if bs, ok := sc.building[n.name]; ok {
			bs.node = n
			delete(sc.building, n.name)
			close(bs.done)
		}
		sc.mu.Unlock()
	}
}

// rollback 在解析失败时：
//  1. 通知等待本尝试暂存作用域实例的同批等待者（统一错误）；
//  2. 按构造逆序释放本次新建且未提交的节点；
//  3. 从各归属作用域摘除已释放节点。
func (a *attempt) rollback() {
	// 先关闭暂存作用域实例的单飞通道，避免等待者拿到正在被释放的节点。
	for _, n := range a.created {
		if n.reg.Lifetime != Scoped || n.committed || n.attempt != a {
			continue
		}
		sc := n.cacheScope
		sc.mu.Lock()
		if bs, ok := sc.building[n.name]; ok && bs.node == nil {
			bs.err = errRolledBack
			delete(sc.building, n.name)
			close(bs.done)
		}
		sc.mu.Unlock()
	}

	disposed := map[*node]bool{}
	for i := len(a.created) - 1; i >= 0; i-- {
		n := a.created[i]
		if n.committed || disposed[n] {
			continue
		}
		disposeNode(n, disposed)
	}
}

// disposeNode 先释放节点俘获的未提交依赖，再释放节点自身。
func disposeNode(n *node, disposed map[*node]bool) {
	if disposed[n] {
		return
	}
	disposed[n] = true
	for i := len(n.deps) - 1; i >= 0; i-- {
		d := n.deps[i]
		if !d.committed {
			disposeNode(d, disposed)
		}
	}
	if d, ok := n.value.(Disposer); ok {
		d.Dispose()
	}
	detachOwned(n)
}
