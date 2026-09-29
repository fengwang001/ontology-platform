package di

import (
	"fmt"
	"sync"
)

// entry is the resolution context: either the root container or a scope.
type entry struct {
	c *Container
	s *Scope // nil when resolving from the root
}

// beginResolve registers one in-flight resolution. It returns a cleanup
// function and false when new resolutions must be rejected (not frozen,
// closed, or shutdown in progress).
func (e entry) beginResolve() (func(), bool) {
	if e.s != nil {
		s := e.s
		s.mu.Lock()
		if s.closed || s.closing {
			s.mu.Unlock()
			return nil, false
		}
		s.active++
		s.mu.Unlock()
		return func() {
			s.mu.Lock()
			s.active--
			if s.closing && s.active == 0 {
				s.cond.Broadcast()
			}
			s.mu.Unlock()
		}, true
	}
	c := e.c
	c.mu.Lock()
	if c.closed || c.closing || !c.frozen {
		c.mu.Unlock()
		return nil, false
	}
	c.active++
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.active--
		if c.closing && c.active == 0 {
			c.cond.Broadcast()
		}
		c.mu.Unlock()
	}, true
}

// rootScoped reports whether the graph rooted at name reaches a scoped
// service. It runs before any instance is created and only matters when
// the root container is the entry point.
func (c *Container) rootScoped(name string) bool {
	visited := make(map[string]bool)
	var walk func(string) bool
	walk = func(n string) bool {
		if visited[n] {
			return false
		}
		visited[n] = true
		reg := c.regs[n]
		if reg.lifetime == Scoped {
			return true
		}
		for _, dep := range reg.deps {
			if walk(dep) {
				return true
			}
		}
		return false
	}
	return walk(name)
}

// Resolve resolves a service graph rooted at name from the root container.
func (c *Container) Resolve(name string) (any, error) {
	c.mu.Lock()
	if c.closed || c.closing {
		c.mu.Unlock()
		return nil, ErrContainerClosed
	}
	if !c.frozen {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: freeze required before resolving %s", ErrNotFrozen, name)
	}
	reg, ok := c.regs[name]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, name)
	}
	if reg.lifetime == Scoped || c.rootScoped(name) {
		return nil, fmt.Errorf("%w: %s", ErrScopedFromRoot, name)
	}

	end, ok := entry{c: c}.beginResolve()
	if !ok {
		return nil, ErrContainerClosed
	}
	defer end()

	val, recs, err := entry{c: c}.build(name)
	if err != nil {
		return nil, err
	}
	// Only a top-level transient reaches here uncommitted; singleton and
	// scoped leaders commit their own records.
	for i := range recs {
		c.commitOwned(recs[i])
	}
	return val, nil
}

// NewScope creates a child resolution scope.
func (c *Container) NewScope() (*Scope, error) {
	c.mu.Lock()
	if c.closed || c.closing {
		c.mu.Unlock()
		return nil, ErrContainerClosed
	}
	if !c.frozen {
		c.mu.Unlock()
		return nil, ErrNotFrozen
	}
	s := &Scope{
		container: c,
		scoped:    make(map[string]any),
		building:  make(map[string]*inflight),
		done:      make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	c.scopesMu.Lock()
	c.scopes[s] = struct{}{}
	c.scopesMu.Unlock()
	c.mu.Unlock()
	return s, nil
}

// Resolve resolves a service graph rooted at name within the scope.
func (s *Scope) Resolve(name string) (any, error) {
	c := s.container
	c.mu.Lock()
	if c.closed || c.closing {
		c.mu.Unlock()
		return nil, ErrContainerClosed
	}
	if !c.frozen {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: freeze required before resolving %s", ErrNotFrozen, name)
	}
	_, ok := c.regs[name]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, name)
	}

	end, ok := entry{s: s}.beginResolve()
	if !ok {
		return nil, ErrScopeClosed
	}
	defer end()

	val, recs, err := entry{s: s}.build(name)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		s.commitOwned(recs[i])
	}
	return val, nil
}

// build resolves name. On success it returns the instance plus the
// creation-order records of newly built uncommitted instances the caller
// now owns (transient chains). Singleton and scoped leaders commit their
// own chains; on failure every newly created instance in the chain is
// released in reverse creation order and nothing is cached.
func (e entry) build(name string) (any, []record, error) {
	if e.s != nil {
		switch e.s.container.regs[name].lifetime {
		case Singleton:
			val, err := e.s.container.buildSingleton(name)
			return val, nil, err
		case Scoped:
			val, err := e.buildScoped(name)
			return val, nil, err
		case Transient:
			return e.buildTransient(name)
		}
	}
	reg := e.c.regs[name]
	switch reg.lifetime {
	case Singleton:
		val, err := e.c.buildSingleton(name)
		return val, nil, err
	case Transient:
		return e.buildTransient(name)
	}
	panic("di: scoped service reached in root build")
}

func (c *Container) buildSingleton(name string) (any, error) {
	c.mu.Lock()
	if val, ok := c.singles[name]; ok {
		c.mu.Unlock()
		return val, nil
	}
	if inf, ok := c.building[name]; ok {
		c.mu.Unlock()
		<-inf.done
		return inf.val, inf.err
	}
	inf := &inflight{done: make(chan struct{})}
	c.building[name] = inf
	reg := c.regs[name]
	c.mu.Unlock()

	val, recs, err := entry{c: c}.buildTransientLike(reg)

	c.mu.Lock()
	delete(c.building, name)
	if err != nil {
		inf.err = err
		c.mu.Unlock()
		close(inf.done)
		releaseReverse(recs)
		return nil, err
	}
	inf.val = val
	c.singles[name] = val
	c.owned = append(c.owned, recs...)
	c.mu.Unlock()
	close(inf.done)
	return val, nil
}

func (e entry) buildScoped(name string) (any, error) {
	s := e.s
	s.mu.Lock()
	if val, ok := s.scoped[name]; ok {
		s.mu.Unlock()
		return val, nil
	}
	if inf, ok := s.building[name]; ok {
		s.mu.Unlock()
		<-inf.done
		return inf.val, inf.err
	}
	inf := &inflight{done: make(chan struct{})}
	s.building[name] = inf
	reg := s.container.regs[name]
	s.mu.Unlock()

	val, recs, err := e.buildTransientLike(reg)

	s.mu.Lock()
	delete(s.building, name)
	if err != nil {
		inf.err = err
		s.mu.Unlock()
		close(inf.done)
		releaseReverse(recs)
		return nil, err
	}
	inf.val = val
	s.scoped[name] = val
	s.owned = append(s.owned, recs...)
	s.mu.Unlock()
	close(inf.done)
	return val, nil
}

// buildTransient constructs a transient instance and returns it together
// with the whole chain (dependencies first, the instance last).
func (e entry) buildTransient(name string) (any, []record, error) {
	return e.buildTransientLike(e.registry(name))
}

func (e entry) buildTransientLike(reg *registration) (any, []record, error) {
	var recs []record
	deps := make(map[string]any, len(reg.deps))
	for _, depName := range reg.deps {
		depVal, depRecs, err := e.build(depName)
		if err != nil {
			releaseReverse(recs)
			return nil, nil, err
		}
		deps[depName] = depVal
		recs = append(recs, depRecs...)
	}

	instance, release, err := safeCall(reg, deps)
	if err != nil {
		releaseReverse(recs)
		return nil, nil, &ConstructionError{Service: reg.name, Err: err}
	}
	recs = append(recs, record{name: reg.name, value: instance, release: release})
	return instance, recs, nil
}

func (e entry) registry(name string) *registration {
	if e.s != nil {
		return e.s.container.regs[name]
	}
	return e.c.regs[name]
}

func (c *Container) commitOwned(rec record) {
	c.mu.Lock()
	c.owned = append(c.owned, rec)
	c.mu.Unlock()
}

func (s *Scope) commitOwned(rec record) {
	s.mu.Lock()
	s.owned = append(s.owned, rec)
	s.mu.Unlock()
}

func releaseReverse(recs []record) {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].release != nil {
			recs[i].release()
		}
	}
}
