package mirror

import "sync"

// Volume 是多成员镜像块卷。全部方法可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个串行顺序。
type Volume struct {
	mu         sync.Mutex
	members    []*member
	gen        uint64 // 卷世代，初始为 1，只在成员新故障时加一
	numBlocks  int
	dirtyLimit int
	blockOps   int64 // 逐块操作计数，用于复杂度可验证性（测试用）
}

// NewVolume 创建卷：members 为成员数（2 到 4），blocks 为逻辑块数，
// dirtyLimit 为脏区上限（块数）。初始全部成员在线、世代为 1、所有块为空值。
func NewVolume(members, blocks, dirtyLimit int) (*Volume, error) {
	if members < 2 || members > 4 {
		return nil, newErr(ErrInvalidArg, "成员数须在 2 到 4 之间，得到 %d", members)
	}
	if blocks <= 0 {
		return nil, newErr(ErrInvalidArg, "块数须为正，得到 %d", blocks)
	}
	if dirtyLimit < 0 {
		return nil, newErr(ErrInvalidArg, "脏区上限须非负，得到 %d", dirtyLimit)
	}
	v := &Volume{
		gen:        1,
		numBlocks:  blocks,
		dirtyLimit: dirtyLimit,
	}
	for i := 0; i < members; i++ {
		v.members = append(v.members, newMember(blocks))
	}
	return v, nil
}

// Write 写入一个块。failed 由调用方注入，表示本次写失败的成员集合。
//
// 写入须在全部在线成员与重同步中成员上完成；只要至少一个在线成员
// 写成功即视为成功，写失败的成员随即转为故障。若所有在线成员都写失败，
// 写入被拒绝并报卷不可用，且不改变任何状态、世代与脏区。
// 一次写入中多个成员同时新故障，世代只加一。
//
// 开销只随成员数增长：逐块操作仅为每个目标成员各写一次。
func (v *Volume) Write(block int, value uint64, failed map[int]bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if block < 0 || block >= v.numBlocks {
		return newErr(ErrInvalidArg, "块号 %d 越界 [0,%d)", block, v.numBlocks)
	}
	for id := range failed {
		if id < 0 || id >= len(v.members) {
			return newErr(ErrNoSuchMember, "写失败集合含不存在的成员 %d", id)
		}
	}

	// 预检：全部在线成员都写失败（或根本没有在线成员）时拒绝，
	// 不得改变任何状态、世代与脏区。
	anyOnlineSuccess := false
	for id, m := range v.members {
		if m.state == MemberOnline && !failed[id] {
			anyOnlineSuccess = true
			break
		}
	}
	if !anyOnlineSuccess {
		return newErr(ErrVolumeUnavailable, "全部在线成员写失败，写入被拒绝")
	}

	// 应用写入：在线与重同步中成员都是目标；写失败者转为故障。
	newlyFaulted := false
	for id, m := range v.members {
		switch m.state {
		case MemberOnline, MemberResyncing:
			if failed[id] {
				m.becomeFaulted(v.gen, v.dirtyLimit)
				newlyFaulted = true
				continue
			}
			m.data[block] = value
			v.blockOps++
			if m.state == MemberResyncing {
				// 写入直接落到重同步中成员，并清除对应块的待同步标记。
				delete(m.pending, block)
			}
		}
	}
	// 一次写入中同时多个成员新故障，世代只加一。
	if newlyFaulted {
		v.gen++
	}
	// 把被写块登记进全部故障成员的脏区记录。
	for _, m := range v.members {
		if m.state == MemberFaulted {
			m.dirty.add(block)
		}
	}
	return nil
}

// Read 读取一个块，由编号最小的在线成员服务。
// 重同步中与故障的成员不服务读；没有在线成员时报卷不可用；
// 从未写过的块得到空值（0）而不是错误。
func (v *Volume) Read(block int) (uint64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if block < 0 || block >= v.numBlocks {
		return 0, newErr(ErrInvalidArg, "块号 %d 越界 [0,%d)", block, v.numBlocks)
	}
	for _, m := range v.members {
		if m.state == MemberOnline {
			v.blockOps++
			return m.data[block], nil
		}
	}
	return 0, newErr(ErrVolumeUnavailable, "没有在线成员，读不可用")
}

// ReportFault 由调用方直接报告某成员故障。
// 该成员须为在线或重同步中，否则报状态不符；世代加一并记录故障世代。
// 若被报告者是最后一个在线成员，重同步中的成员一并转为故障，
// 各自记录同一故障世代，世代仍只加一。
func (v *Volume) ReportFault(id int) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if id < 0 || id >= len(v.members) {
		return newErr(ErrNoSuchMember, "成员 %d 不存在", id)
	}
	m := v.members[id]
	if m.state == MemberFaulted {
		return newErr(ErrBadState, "成员 %d 已是故障状态", id)
	}

	wasLastOnline := m.state == MemberOnline && v.onlineCountLocked() == 1
	faultGen := v.gen
	v.gen++
	m.becomeFaulted(faultGen, v.dirtyLimit)
	if wasLastOnline {
		// 最后一个在线成员故障，重同步中成员一并转为故障，
		// 记录同一故障世代，世代不再额外增加。
		for _, other := range v.members {
			if other.state == MemberResyncing {
				other.becomeFaulted(faultGen, v.dirtyLimit)
			}
		}
	}
	return nil
}

func (v *Volume) onlineCountLocked() int {
	n := 0
	for _, m := range v.members {
		if m.state == MemberOnline {
			n++
		}
	}
	return n
}

// Snapshot 返回整个卷当前时刻的完整可复现状态。
func (v *Volume) Snapshot() Snapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.snapshotLocked()
}

func (v *Volume) snapshotLocked() Snapshot {
	snap := Snapshot{Generation: v.gen, Members: make([]MemberSnapshot, len(v.members))}
	for i, m := range v.members {
		ms := MemberSnapshot{
			State:    m.state,
			FaultGen: m.faultGen,
			Pending:  m.pendingSorted(),
			Data:     append([]uint64(nil), m.data...),
		}
		if m.dirty != nil {
			ms.DirtyDropped = m.dirty.dropped
		}
		snap.Members[i] = ms
	}
	return snap
}

// BlockOps 返回累计逐块操作计数，用于验证写与部分重同步的开销
// 只随成员数 / 脏区块数增长，与总块数无关。
func (v *Volume) BlockOps() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.blockOps
}
