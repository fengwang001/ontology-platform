package mirror

// member 是卷的一个成员，持有自己的一份完整块数据。
type member struct {
	state    MemberState
	data     []uint64
	faultGen uint64 // 最近一次转为故障时（转换前）卷的世代

	dirty *dirtyLog // 非在线成员持有；在线成员为 nil

	// 以下字段仅重同步中有效：plan 为待复制块的升序序列，
	// pending 为尚未完成的块集合（写入可直接清除其中标记）。
	plan    []int
	cursor  int
	pending map[int]struct{}
}

func newMember(numBlocks int) *member {
	return &member{
		state: MemberOnline,
		data:  make([]uint64, numBlocks),
	}
}

// becomeFaulted 将在线或重同步中的成员转为故障，记录故障世代。
// 重同步中成员的待同步集合原样保留为其脏区记录（含超限标记）。
func (m *member) becomeFaulted(faultGen uint64, dirtyLimit int) {
	if m.state == MemberResyncing {
		dl := newDirtyLog(dirtyLimit)
		dl.set = m.pending
		dl.dropped = m.dirty.dropped
		m.dirty = dl
	} else {
		m.dirty = newDirtyLog(dirtyLimit)
	}
	m.plan = nil
	m.cursor = 0
	m.pending = nil
	m.state = MemberFaulted
	m.faultGen = faultGen
}

// beginResync 以给定的升序块计划进入重同步中状态。
func (m *member) beginResync(plan []int) {
	m.state = MemberResyncing
	m.plan = plan
	m.cursor = 0
	m.pending = make(map[int]struct{}, len(plan))
	for _, b := range plan {
		m.pending[b] = struct{}{}
	}
}

// finishResync 重同步完成，转为在线，脏区记录随之清除（含超限标记）。
func (m *member) finishResync() {
	m.state = MemberOnline
	m.dirty = nil
	m.plan = nil
	m.cursor = 0
	m.pending = nil
}

// pendingSorted 返回待同步块集合的升序副本，供快照使用。
func (m *member) pendingSorted() []int {
	switch m.state {
	case MemberResyncing:
		out := make([]int, 0, len(m.pending))
		for _, b := range m.plan {
			if _, ok := m.pending[b]; ok {
				out = append(out, b)
			}
		}
		return out
	case MemberFaulted:
		return m.dirty.sorted()
	default:
		return nil
	}
}
