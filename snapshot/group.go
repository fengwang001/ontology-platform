package snapshot

import "sort"

// group 是一个一致性组的内部状态。同一时刻最多一个进行中的快照，
// 因此快照状态直接内嵌在组上。
type group struct {
	id string

	members   map[string]*volume
	memberIDs []string // 始终按卷标识排序，保证遍历确定

	phase Phase

	// 冻结中阶段：冻结截止时刻与尚未确认的卷数。
	freezeDeadline Time
	remaining      int

	// 已冻结阶段：快照点（最后一个确认发生的时刻）。
	snapshotPoint Time

	// records 是该组已提交的快照记录。
	records []SnapshotRecord
}

func newGroup(id string) *group {
	return &group{id: id, members: make(map[string]*volume), phase: PhaseIdle}
}

// insertMember 维护 memberIDs 有序。
func (g *group) insertMember(v *volume) {
	g.members[v.id] = v
	i := sort.SearchStrings(g.memberIDs, v.id)
	g.memberIDs = append(g.memberIDs, "")
	copy(g.memberIDs[i+1:], g.memberIDs[i:])
	g.memberIDs[i] = v.id
}

func (g *group) removeMember(id string) {
	delete(g.members, id)
	i := sort.SearchStrings(g.memberIDs, id)
	if i < len(g.memberIDs) && g.memberIDs[i] == id {
		g.memberIDs = append(g.memberIDs[:i], g.memberIDs[i+1:]...)
	}
}

// confirmedIDs 返回冻结中阶段已确认卷（排序），供视图与调试使用。
func (g *group) confirmedIDs() []string {
	var out []string
	if g.phase != PhaseFreezing {
		return out
	}
	for _, id := range g.memberIDs {
		if g.members[id].confirmed {
			out = append(out, id)
		}
	}
	return out
}

// cutoffs 返回各卷当前写序号快照。已冻结阶段各卷写序号不再变化，
// 因此它等于快照点的各卷截止点（延迟捕获，保证确认冻结为常数开销）。
func (g *group) cutoffs() map[string]uint64 {
	out := make(map[string]uint64, len(g.members))
	for id, v := range g.members {
		out[id] = v.seq
	}
	return out
}

func (g *group) view() GroupView {
	members := make([]string, len(g.memberIDs))
	copy(members, g.memberIDs)
	view := GroupView{
		ID:             g.id,
		Phase:          g.phase,
		Members:        members,
		Confirmed:      g.confirmedIDs(),
		FreezeDeadline: g.freezeDeadline,
		SnapshotPoint:  g.snapshotPoint,
		Records:        append([]SnapshotRecord(nil), g.records...),
	}
	if g.phase == PhaseFreezing {
		view.RemainingConfirmations = g.remaining
	}
	if g.phase == PhaseFrozen {
		view.Cutoffs = g.cutoffs()
	}
	return view
}
