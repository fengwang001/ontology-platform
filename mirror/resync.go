package mirror

// Rejoin 成员重新加入，diskGen 为盘面自带的世代标签。
//
// 标签大于卷当前世代报世代超前且不改变状态；标签等于该成员记录的
// 故障世代且脏区记录有效时只重同步脏区内的块，否则一律全量重同步。
// 全部成员都处于故障时，只允许故障世代最大的成员（并列取编号最小者）
// 作为首个重新加入者，其数据视为权威，加入后立即在线、无需同步；
// 其余成员此后重新加入一律全量重同步。
func (v *Volume) Rejoin(id int, diskGen uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if id < 0 || id >= len(v.members) {
		return newErr(ErrNoSuchMember, "成员 %d 不存在", id)
	}
	m := v.members[id]
	if m.state != MemberFaulted {
		return newErr(ErrBadState, "成员 %d 当前为%s，只有故障成员可重新加入", id, m.state)
	}
	if diskGen > v.gen {
		return newErr(ErrGenerationAhead, "盘面世代标签 %d 超过卷当前世代 %d", diskGen, v.gen)
	}

	if v.allFaultedLocked() {
		// 卷无权威数据：只允许故障世代最大者（并列取编号最小者）首先重新加入。
		auth := v.authoritativeLocked()
		if id != auth {
			return newErr(ErrNotAuthoritative,
				"全部成员故障，首个重新加入者须为权威成员 %d，得到 %d", auth, id)
		}
		// 权威成员数据视为权威：立即在线、无需同步，脏区记录清除。
		m.state = MemberOnline
		m.dirty = nil
		// 其余成员此后重新加入一律全量重同步，无论标签是否相等。
		for _, other := range v.members {
			if other.state == MemberFaulted {
				other.dirty.dropped = true
			}
		}
		return nil
	}

	// 卷已有权威数据：按标签与脏区记录决定部分或全量重同步。
	var plan []int
	if diskGen == m.faultGen && !m.dirty.dropped {
		// 部分重同步：选块开销只随脏区内块数增长。
		plan = m.dirty.sorted()
	} else {
		plan = allBlocks(v.numBlocks)
	}
	m.beginResync(plan)
	return nil
}

// AdvanceResync 按调用方给出的上限批量推进重同步，
// 按块号升序复制，返回本次实际复制的块号。
// 复制完成最后一块的那一次推进使成员转为在线。
func (v *Volume) AdvanceResync(id int, limit int) ([]int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if limit <= 0 {
		return nil, newErr(ErrInvalidArg, "批量上限须为正，得到 %d", limit)
	}
	if id < 0 || id >= len(v.members) {
		return nil, newErr(ErrNoSuchMember, "成员 %d 不存在", id)
	}
	m := v.members[id]
	if m.state != MemberResyncing {
		return nil, newErr(ErrBadState, "成员 %d 当前为%s，不在重同步中", id, m.state)
	}

	// 不变式：存在重同步中成员时必有在线成员作为复制源
	// （最后一个在线成员故障时重同步中成员会一并转为故障）。
	src := v.smallestOnlineLocked()
	copied := make([]int, 0, limit)
	for m.cursor < len(m.plan) && len(copied) < limit {
		b := m.plan[m.cursor]
		m.cursor++
		if _, ok := m.pending[b]; !ok {
			continue // 该块已被重同步期间的写入直接覆盖
		}
		m.data[b] = src.data[b]
		v.blockOps++
		delete(m.pending, b)
		copied = append(copied, b)
	}
	if len(m.pending) == 0 {
		m.finishResync()
	}
	return copied, nil
}

func (v *Volume) allFaultedLocked() bool {
	for _, m := range v.members {
		if m.state != MemberFaulted {
			return false
		}
	}
	return true
}

// authoritativeLocked 返回全部故障时的权威成员：
// 故障世代最大者，并列取编号最小者。
func (v *Volume) authoritativeLocked() int {
	auth := 0
	for i, m := range v.members {
		if m.faultGen > v.members[auth].faultGen {
			auth = i
		}
	}
	return auth
}

func (v *Volume) smallestOnlineLocked() *member {
	for _, m := range v.members {
		if m.state == MemberOnline {
			return m
		}
	}
	return nil
}

func allBlocks(n int) []int {
	plan := make([]int, n)
	for i := range plan {
		plan[i] = i
	}
	return plan
}
