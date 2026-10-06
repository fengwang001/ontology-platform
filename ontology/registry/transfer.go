package registry

// Transfer 将一批证书整体从 from 转给 to。
// 先批级参数检查，再按序号升序逐张判定，任一失败整批不变。
func (r *Registry) Transfer(from, to string, serials []int64) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if e := r.batchParamCheck(from, to, serials); e != nil {
		r.logf("Transfer(%q->%q,%v) -> %s", from, to, serials, e)
		return e
	}
	ordered := sortedUnique(serials)
	for _, s := range ordered {
		c := r.certs[s]
		if c == nil {
			return r.transferFail(from, to, serials, errAt(CodeInvalidParam, s, "证书序号不存在"))
		}
		if c.Status != StatusHeld {
			return r.transferFail(from, to, serials, errAt(CodeStateNotAllowed, s, "证书非持有状态"))
		}
		if c.Holder != from {
			return r.transferFail(from, to, serials, errAt(CodeNotHolder, s, "证书非转让方持有"))
		}
	}
	for _, s := range ordered {
		r.certs[s].Holder = to
	}
	r.emit(Event{Kind: EventTransferred, Holder: from, ToHolder: to, Serials: append([]int64(nil), ordered...)})
	r.logf("Transfer(%q->%q,%v) -> ok", from, to, ordered)
	return nil
}

func (r *Registry) transferFail(from, to string, serials []int64, e *Error) *Error {
	r.logf("Transfer(%q->%q,%v) -> %s", from, to, serials, e)
	return e
}

// batchParamCheck 批级参数：空/自转、序号非正或重复（重复不合法，因一张证不能整体转让两次）。
func (r *Registry) batchParamCheck(from, to string, serials []int64) *Error {
	if len(serials) == 0 {
		return errf(CodeInvalidParam, "批数量非正")
	}
	if from == "" || to == "" {
		return errf(CodeInvalidParam, "持有人为空")
	}
	if from == to {
		return errf(CodeInvalidParam, "自转让非法")
	}
	seen := make(map[int64]struct{}, len(serials))
	for _, s := range serials {
		if s <= 0 {
			return errAt(CodeInvalidParam, s, "证书序号非正")
		}
		if _, ok := seen[s]; ok {
			return errAt(CodeInvalidParam, s, "批内序号重复")
		}
		seen[s] = struct{}{}
	}
	return nil
}

// sortedUnique 返回去重后升序副本。调用方已保证无重复。
func sortedUnique(serials []int64) []int64 {
	out := append([]int64(nil), serials...)
	// 插入排序足以应对常规批量；为保证可预测性这里使用原地堆排序。
	heapSort(out)
	return out
}

func heapSort(a []int64) {
	n := len(a)
	down := func(root, size int) {
		for {
			l, best := 2*root+1, root
			if l < size && a[l] > a[best] {
				best = l
			}
			if l+1 < size && a[l+1] > a[best] {
				best = l + 1
			}
			if best == root {
				return
			}
			a[root], a[best] = a[best], a[root]
			root = best
		}
	}
	for i := n/2 - 1; i >= 0; i-- {
		down(i, n)
	}
	for i := n - 1; i > 0; i-- {
		a[0], a[i] = a[i], a[0]
		down(0, i)
	}
}
