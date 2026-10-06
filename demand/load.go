package demand

import "sort"

// LoadSpec 描述一个可控负荷的静态属性。
type LoadSpec struct {
	// ID 负荷编号，在控制器内唯一。
	ID int
	// RatedKW 额定功率（千瓦，正整数）。
	RatedKW int64
	// Priority 优先级数字，越大越不重要；现有负荷中最小数字为关键负荷。
	Priority int
	// MinOnSec 最短接入时长（秒，非负整数）。
	MinOnSec int64
	// MinOffSec 最短断开时长（秒，非负整数）。
	MinOffSec int64
}

func (s LoadSpec) validate() error {
	if s.ID <= 0 {
		return errParam("负荷编号必须为正整数，实际为 %d", s.ID)
	}
	if s.RatedKW <= 0 {
		return errParam("负荷 %d 额定功率必须为正整数", s.ID)
	}
	if s.Priority <= 0 {
		return errParam("负荷 %d 优先级必须为正整数", s.ID)
	}
	if s.MinOnSec < 0 || s.MinOffSec < 0 {
		return errParam("负荷 %d 最短接入/断开时长不得为负", s.ID)
	}
	return nil
}

// loadState 为负荷的运行态。
type loadState struct {
	spec   LoadSpec
	on     bool  // 当前是否接入
	locked bool  // 是否被运维锁定为保持接入
	since  int64 // 最近一次状态切换时刻（接入或断开）
}

// loadBook 管理全部负荷。
type loadBook struct {
	byID map[int]*loadState
}

func newLoadBook() *loadBook { return &loadBook{byID: map[int]*loadState{}} }

// add 增加负荷（初始接入）；编号重复属于参数非法。
func (b *loadBook) add(at int64, spec LoadSpec) error {
	if err := spec.validate(); err != nil {
		return err
	}
	if _, ok := b.byID[spec.ID]; ok {
		return errParam("负荷编号 %d 已存在", spec.ID)
	}
	b.byID[spec.ID] = &loadState{spec: spec, on: true, locked: false, since: at}
	return nil
}

func (b *loadBook) get(id int) (*loadState, bool) {
	l, ok := b.byID[id]
	return l, ok
}

// criticalPriority 返回现有负荷中最小的优先级数字；无负荷时为 0。
// 该数字对应关键负荷，永不被切除。
func (b *loadBook) criticalPriority() int {
	best := 0
	for _, l := range b.byID {
		if best == 0 || l.spec.Priority < best {
			best = l.spec.Priority
		}
	}
	return best
}

// all 返回按编号升序的全部负荷。
func (b *loadBook) all() []*loadState {
	out := make([]*loadState, 0, len(b.byID))
	for _, l := range b.byID {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].spec.ID < out[j].spec.ID })
	return out
}

// shedCandidates 返回可切除负荷：当前接入、已满足最短接入、未锁定、非关键。
func (b *loadBook) shedCandidates(now int64) []*loadState {
	crit := b.criticalPriority()
	var out []*loadState
	for _, l := range b.all() {
		if l.locked || !l.on || l.spec.Priority == crit {
			continue
		}
		if l.onFor(now) < l.spec.MinOnSec {
			continue
		}
		out = append(out, l)
	}
	return out
}

// restoreCandidates 返回已断开且已满足最短断开时长、未锁定的负荷。
func (b *loadBook) restoreCandidates(now int64) []*loadState {
	var out []*loadState
	for _, l := range b.all() {
		if l.locked || l.on {
			continue
		}
		if l.offFor(now) < l.spec.MinOffSec {
			continue
		}
		out = append(out, l)
	}
	// 优先级数字从小到大、同级按编号从小到大。
	sort.Slice(out, func(i, j int) bool {
		if out[i].spec.Priority != out[j].spec.Priority {
			return out[i].spec.Priority < out[j].spec.Priority
		}
		return out[i].spec.ID < out[j].spec.ID
	})
	return out
}

// onFor 返回截至 now 已持续接入的时长。
func (l *loadState) onFor(now int64) int64 {
	if !l.on {
		return 0
	}
	return now - l.since
}

// offFor 返回截至 now 已持续断开的时长。
func (l *loadState) offFor(now int64) int64 {
	if l.on {
		return 0
	}
	return now - l.since
}
