package lifecycle

// SetAttrs 在一个处理单元内修改单个实例的属性。终态实例的属性修改被
// 拒绝（ErrTerminal），拒绝不改变任何可观察状态与时钟戳。
func (e *Engine) SetAttrs(id InstanceID, ops ...AttrOp) *LifecycleError {
	unlock := e.store.lockAll([]InstanceID{id})
	defer unlock()
	e.store.RLock()
	snap, ok := e.store.snapshotInstanceLocked(id)
	if !ok {
		e.store.RUnlock()
		return newErr(ErrUndeclared, id, "", "instance does not exist")
	}
	t := e.store.types[snap.Type]
	v := newView(e.store)
	stage := v.ensureStage(snap)
	if err := v.applyAttrs(stage, ops); err != nil {
		e.store.RUnlock()
		return newErr(ErrUndeclared, id, "", err.Error())
	}
	if t.IsTerminal(snap.State) {
		e.store.RUnlock()
		return newErr(ErrTerminal, id, "", "cannot modify attrs of terminal instance")
	}
	e.store.RUnlock()

	e.store.Lock()
	defer e.store.Unlock()
	clock := e.store.nextClockLocked()
	inst := e.store.instances[id]
	inst.Attrs = cloneAttrs(stage.attrs)
	inst.Clock = clock
	inst.Version++
	if e.log != nil {
		e.log.Record(LogEntry{
			Request:   TransitionRequest{Instance: id, Attrs: ops},
			Accepted:  true,
			FromState: snap.State,
			ToState:   snap.State,
			Reasons:   []string{"standalone attr update"},
		})
	}
	return nil
}

// ModifyLinks 在一个处理单元内变更单个实例的出向链接。终态实例不允许
// 新增链接，但允许删除已有链接（LinkDel）。
func (e *Engine) ModifyLinks(id InstanceID, ops ...LinkOp) *LifecycleError {
	unlock := e.store.lockAll([]InstanceID{id})
	defer unlock()
	e.store.RLock()
	snap, ok := e.store.snapshotInstanceLocked(id)
	if !ok {
		e.store.RUnlock()
		return newErr(ErrUndeclared, id, "", "instance does not exist")
	}
	t := e.store.types[snap.Type]
	v := newView(e.store)
	stage := v.ensureStage(snap)

	// 终态保护：终态实例不允许新增链接；删除已有链接放行。
	if t.IsTerminal(snap.State) {
		for _, op := range ops {
			if op.Op == LinkAdd {
				e.store.RUnlock()
				return newErr(ErrTerminal, id, "", "cannot add links from terminal instance")
			}
		}
	}
	// 目标实例必须存在。
	for _, op := range ops {
		if _, exists := e.store.instances[op.Target]; !exists {
			e.store.RUnlock()
			return newErr(ErrUndeclared, op.Target, "", "link target instance does not exist")
		}
	}
	if err := v.applyLinks(stage, ops); err != nil {
		e.store.RUnlock()
		return newErr(ErrUndeclared, id, "", err.Error())
	}
	e.store.RUnlock()

	e.store.Lock()
	defer e.store.Unlock()
	clock := e.store.nextClockLocked()
	e.applyLinksCommitLocked(id, stage)
	inst := e.store.instances[id]
	inst.Clock = clock
	inst.Version++
	if e.log != nil {
		e.log.Record(LogEntry{
			Request:   TransitionRequest{Instance: id, Links: ops},
			Accepted:  true,
			FromState: snap.State,
			ToState:   snap.State,
			Reasons:   []string{"standalone link update"},
		})
	}
	return nil
}
