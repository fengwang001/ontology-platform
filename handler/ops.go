package handler

// lookup 返回实例（不存在返回 nil, ErrNotFound）。
func (h *Handler) lookup(instance []byte) (*inst, error) {
	h.mu.RLock()
	st := h.m[string(instance)]
	h.mu.RUnlock()
	if st == nil {
		return nil, ErrNotFound
	}
	return st, nil
}

// Step 应用队首更新；空队列返回 ErrEmpty。
func (h *Handler) Step(instance []byte) error {
	if len(instance) == 0 {
		return ErrInvalidParam
	}
	st, err := h.lookup(instance)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.queue) == 0 {
		return ErrEmpty
	}
	head := st.queue[0]
	st.queue = st.queue[1:]
	st.s += head.delta
	st.log.AppendApplied(head.seq)
	st.dedup.Put(head.uid, Result{Kind: Completed, Val: st.s})
	return nil
}

// Close 幂等关闭实例：首次追加 C，未应用更新全部 Aborted。
func (h *Handler) Close(instance []byte) error {
	if len(instance) == 0 {
		return ErrInvalidParam
	}
	st, err := h.lookup(instance)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	first, _ := st.log.AppendClosed() // 幂等：仅首次真正写入
	if !first {
		return nil
	}
	st.closed = true
	for _, p := range st.queue {
		st.dedup.Put(p.uid, Result{Kind: Aborted})
	}
	st.queue = nil
	return nil
}

// Result 返回 uid 当前登记结果；去重表中没有为 Unknown。
func (h *Handler) Result(instance, uid []byte) (Result, error) {
	if len(instance) == 0 || len(uid) == 0 {
		return Result{}, ErrInvalidParam
	}
	st, err := h.lookup(instance)
	if err != nil {
		return Result{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if r, ok := st.dedup.Lookup(uid); ok {
		return r.(Result), nil
	}
	return Result{Kind: Unknown}, nil
}
