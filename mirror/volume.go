package mirror

import (
	"fmt"
	"sync"
)

// Stats 统计一次或多次操作中的底层工作量，用于以外部可验证的
// 方式证明写与部分重同步选块的开销不随总块数增长。
type Stats struct {
	BlockDataOps uint64 // 成员盘面块级读写次数
	DirtyElemOps uint64 // 脏区记录元素级访问次数
}

// MemberSnapshot 精确复现某一时刻单个成员的全部相关状态。
type MemberSnapshot struct {
	State      MemberState
	FaultGen   uint64
	FullResync bool
	Pending    []int // 待同步块集合（升序）；在线成员为 nil
	Data       []string
}

// Snapshot 精确复现某一时刻整卷状态：世代与每个成员的状态、
// 故障世代、待同步块集合与盘面数据。
type Snapshot struct {
	Generation uint64
	Members    []MemberSnapshot
}

// Volume 是多成员镜像块卷。全部方法持有同一把互斥锁，
// 并发调用等价于某个串行顺序（可线性化）。
type Volume struct {
	mu        sync.Mutex
	numBlocks int
	limit     int
	gen       uint64
	members   []*member
	stats     Stats
}

// NewVolume 创建卷：members 为成员数（2 到 4），numBlocks 为逻辑块数，
// dirtyLimit 为脏区上限（以块计）。世代初始为 1，全部成员在线、数据为空。
func NewVolume(members, numBlocks, dirtyLimit int) (*Volume, error) {
	if members < 2 || members > 4 {
		return nil, errf(ErrInvalidArgument, "成员数须在 2 到 4 之间，得到 %d", members)
	}
	if numBlocks < 1 {
		return nil, errf(ErrInvalidArgument, "块数须为正，得到 %d", numBlocks)
	}
	if dirtyLimit < 0 {
		return nil, errf(ErrInvalidArgument, "脏区上限须非负，得到 %d", dirtyLimit)
	}
	v := &Volume{numBlocks: numBlocks, limit: dirtyLimit, gen: 1}
	for i := 0; i < members; i++ {
		v.members = append(v.members, &member{
			state: Online,
			data:  make([]string, numBlocks),
		})
	}
	return v, nil
}

// Write 把 value 写入块 block。failed 为调用方注入的本次写失败成员集合。
//
// 写入在全部在线成员与重同步中成员上执行；至少一个在线成员写成功
// 才算成功。写失败的成员转为故障并记录故障世代（转换前的卷世代），
// 同一次写入中多个成员新故障世代只加一。若全部在线成员都写失败，
// 写入被整体拒绝，不改变任何状态、世代与脏区。
// 成功的写入把该块登记进全部故障成员的脏区记录，并清除重同步中
// 成员对应块的待同步标记。
func (v *Volume) Write(block int, value string, failed []int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if block < 0 || block >= v.numBlocks {
		return errf(ErrInvalidArgument, "块号 %d 超出 [0,%d)", block, v.numBlocks)
	}
	failedSet := make(map[int]bool, len(failed))
	for _, id := range failed {
		if id < 0 || id >= len(v.members) {
			return errf(ErrNoSuchMember, "写失败集合含不存在的成员 %d", id)
		}
		failedSet[id] = true
	}
	anyOnline := false
	allOnlineFail := true
	for i, m := range v.members {
		if m.state != Online {
			continue
		}
		anyOnline = true
		if !failedSet[i] {
			allOnlineFail = false
		}
	}
	if !anyOnline {
		return errf(ErrVolumeUnavailable, "没有在线成员")
	}
	if allOnlineFail {
		return errf(ErrVolumeUnavailable, "全部在线成员写失败，写入被拒绝")
	}
	anyFault := false
	for i, m := range v.members {
		if m.state != Online && m.state != Resyncing {
			continue
		}
		if failedSet[i] {
			v.faultLocked(m)
			anyFault = true
			continue
		}
		m.data[block] = value
		v.stats.BlockDataOps++
	}
	if anyFault {
		v.gen++
	}
	for _, m := range v.members {
		switch m.state {
		case Faulted:
			m.dirty.add(block)
		case Resyncing:
			m.dirty.remove(block)
		}
	}
	return nil
}

// Read 读取块 block，由编号最小的在线成员服务，返回该成员编号。
// 重同步中与故障的成员不服务读；从未写过的块返回空值。
func (v *Volume) Read(block int) (value string, from int, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if block < 0 || block >= v.numBlocks {
		return "", -1, errf(ErrInvalidArgument, "块号 %d 超出 [0,%d)", block, v.numBlocks)
	}
	for i, m := range v.members {
		if m.state == Online {
			v.stats.BlockDataOps++
			return m.data[block], i, nil
		}
	}
	return "", -1, errf(ErrVolumeUnavailable, "没有在线成员")
}

// ReportFault 由调用方直接报告成员故障。
//
// 被报告者须为在线或重同步中。世代加一，故障世代取转换前的卷世代。
// 若被报告者是最后一个在线成员，重同步中成员一并转为故障并记录
// 同一故障世代，世代仍只加一。
func (v *Volume) ReportFault(id int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if id < 0 || id >= len(v.members) {
		return errf(ErrNoSuchMember, "成员 %d 不存在", id)
	}
	m := v.members[id]
	if m.state != Online && m.state != Resyncing {
		return errf(ErrInvalidState, "成员 %d 处于%s，不能报告故障", id, m.state)
	}
	v.faultLocked(m)
	hasOnline := false
	for _, o := range v.members {
		if o.state == Online {
			hasOnline = true
			break
		}
	}
	if !hasOnline {
		for _, o := range v.members {
			if o.state == Resyncing {
				v.faultLocked(o)
			}
		}
	}
	v.gen++
	return nil
}

// Rejoin 使故障成员重新加入，diskGen 为其盘面自带的世代标签。
//
// 标签大于卷当前世代报世代超前且不改变状态。全部成员均故障时，
// 只允许故障世代最大（并列取编号最小）的成员作为首个重新加入者，
// 其数据视为权威并立即在线，其余成员此后一律全量重同步。
// 否则标签等于该成员故障世代且脏区记录有效时只重同步脏区内的块，
// 否则一律全量重同步。重新加入后成员转为重同步中。
func (v *Volume) Rejoin(id int, diskGen uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if id < 0 || id >= len(v.members) {
		return errf(ErrNoSuchMember, "成员 %d 不存在", id)
	}
	m := v.members[id]
	if m.state != Faulted {
		return errf(ErrInvalidState, "成员 %d 处于%s，不能重新加入", id, m.state)
	}
	if diskGen > v.gen {
		return errf(ErrGenerationAhead, "盘面世代 %d 超过卷世代 %d", diskGen, v.gen)
	}
	allFaulted := true
	for _, o := range v.members {
		if o.state != Faulted {
			allFaulted = false
			break
		}
	}
	if allFaulted {
		best := 0
		for i := 1; i < len(v.members); i++ {
			if v.members[i].faultGen > v.members[best].faultGen {
				best = i
			}
		}
		if id != best {
			return errf(ErrNotAuthoritative,
				"卷无权威数据，成员 %d 的故障世代不是最大（权威成员为 %d）", id, best)
		}
		m.state = Online
		m.dirty = nil
		for i, o := range v.members {
			if i == id {
				continue
			}
			o.dirty = newDirtyLog(v.limit, &v.stats.DirtyElemOps)
			o.dirty.fullResync = true
		}
		return nil
	}
	if diskGen == m.faultGen && !m.dirty.fullResync {
		// 部分重同步：脏区记录即待同步块集合。
	} else {
		m.dirty = newDirtyLog(v.limit, &v.stats.DirtyElemOps)
		m.dirty.fillAll(v.numBlocks)
	}
	m.state = Resyncing
	return nil
}

// ResyncAdvance 按块号升序推进至多 maxBlocks 块，返回实际复制的块号。
// 数据取自编号最小的在线成员；复制完成最后一块的这次推进使成员
// 转为在线，并清除全量重同步标记。
func (v *Volume) ResyncAdvance(id int, maxBlocks int) ([]int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if maxBlocks < 1 {
		return nil, errf(ErrInvalidArgument, "推进上限须为正，得到 %d", maxBlocks)
	}
	if id < 0 || id >= len(v.members) {
		return nil, errf(ErrNoSuchMember, "成员 %d 不存在", id)
	}
	m := v.members[id]
	if m.state != Resyncing {
		return nil, errf(ErrInvalidState, "成员 %d 处于%s，不能推进重同步", id, m.state)
	}
	src := -1
	for i, o := range v.members {
		if o.state == Online {
			src = i
			break
		}
	}
	if src < 0 {
		return nil, errf(ErrVolumeUnavailable, "没有在线成员可作为同步源")
	}
	s := v.members[src]
	copied := make([]int, 0, maxBlocks)
	for len(copied) < maxBlocks {
		b, ok := m.dirty.set.popMin()
		if !ok {
			break
		}
		m.data[b] = s.data[b]
		v.stats.BlockDataOps += 2
		copied = append(copied, b)
	}
	if m.dirty.set.len() == 0 {
		m.state = Online
		m.dirty = nil
	}
	return copied, nil
}

// Snapshot 返回当前完整状态快照。
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
			Data:     append([]string(nil), m.data...),
		}
		if m.dirty != nil {
			ms.FullResync = m.dirty.fullResync
			ms.Pending = m.dirty.set.ascending()
		}
		snap.Members[i] = ms
	}
	return snap
}

// Digest 输出当前完整状态的规范字符串，任意时刻的各成员状态、
// 卷世代与待同步块集合都可由它精确复现，也用于与朴素模型逐位对照。
func (v *Volume) Digest() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	snap := v.snapshotLocked()
	s := fmt.Sprintf("gen=%d", snap.Generation)
	for i, ms := range snap.Members {
		s += fmt.Sprintf("|m%d st=%d fg=%d full=%v pend=", i, int(ms.State), ms.FaultGen, ms.FullResync)
		if ms.Pending == nil {
			s += "-"
		} else {
			s += "["
			for _, b := range ms.Pending {
				s += fmt.Sprintf("%d,", b)
			}
			s += "]"
		}
		s += fmt.Sprintf(" data=%q", ms.Data)
	}
	return s
}

// faultLocked 把成员转为故障：故障世代取转换前的卷世代；
// 重同步中成员保留已有脏区记录，在线成员获得一份新的空记录。
func (v *Volume) faultLocked(m *member) {
	if m.dirty == nil {
		m.dirty = newDirtyLog(v.limit, &v.stats.DirtyElemOps)
	}
	m.faultGen = v.gen
	m.state = Faulted
}

// ResetStats 清零工作量计数器。
func (v *Volume) ResetStats() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stats = Stats{}
}

// Stats 返回当前工作量计数。
func (v *Volume) Stats() Stats {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.stats
}
