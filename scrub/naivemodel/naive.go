// Package naivemodel 是一个独立编写的朴素参考模型。
//
// 与生产实现 scrub.Store 的刻意区别：
//   - 到期选取每次线性扫描全部块（O(块总数)），不维护任何索引；
//   - 仲裁在此文件中用不同的写法（手工选择排序找第 k 大、
//     两遍扫描）重新实现，不调用 scrub.Arbitrate；
//   - 状态表示用并行的 map 字段，而非 blockState 结构。
//
// 两个实现对外暴露同构的操作语义，随机对照测试逐条比对
// 输入、终局分类、状态变化与告警，任何分歧立即失败。
package naivemodel

import "sort"

type Time int64

type Outcome int

const (
	OutcomeNoRepair Outcome = iota
	OutcomeRepaired
	OutcomePartial
	OutcomeNoSource
	OutcomeCommittedLost
	OutcomeConflict
)

func (o Outcome) String() string {
	switch o {
	case OutcomeNoRepair:
		return "no-repair-needed"
	case OutcomeRepaired:
		return "repaired"
	case OutcomePartial:
		return "partial-repair"
	case OutcomeNoSource:
		return "no-available-source"
	case OutcomeCommittedLost:
		return "committed-data-lost"
	case OutcomeConflict:
		return "version-conflict"
	default:
		return "unknown"
	}
}

const (
	ErrInvalid = iota + 1
	ErrClockBack
	ErrNotFound
	ErrTooFrequent
)

type ErrorClass int

func (e ErrorClass) Error() string {
	switch e {
	case ErrInvalid:
		return "naive: invalid argument"
	case ErrClockBack:
		return "naive: clock moved backwards"
	case ErrNotFound:
		return "naive: block not found"
	case ErrTooFrequent:
		return "naive: patrol too frequent"
	default:
		return "naive: ok"
	}
}

type Replica struct {
	Node         int
	Version      int64
	SavedDigest  string
	ActualDigest string
}

type Target struct {
	Node   int
	Bitrot bool
}

type Decision struct {
	Outcome          Outcome
	Quorum           int
	CommittedVersion int64
	AuthorityVersion int64
	AuthorityDigest  string
	Targets          []Target
}

type PatrolResult struct {
	Decision
	Time        Time
	Repaired    []int
	FailedNodes []int
}

type Alert struct {
	Seq     int
	Time    Time
	BlockID int
	Outcome Outcome
}

type block struct {
	reps      map[int]Replica
	interval  Time
	last      Time
	hasPatrol bool
}

type Model struct {
	clock  Time
	blocks map[int]*block
	alerts []Alert
}

func New() *Model {
	return &Model{blocks: make(map[int]*block)}
}

// judge 用与生产代码不同的朴素写法实现仲裁：
// 手工“选 k 次最大值”求第 quorum 大版本，随后两遍扫描分类。
func judge(reps []Replica) Decision {
	d := Decision{Quorum: len(reps)/2 + 1}
	if len(reps) == 0 {
		d.Outcome = OutcomeNoSource
		return d
	}

	// 第 quorum 大：在版本切片上重复挑最大值并置零（副本版本恒正）。
	vs := make([]int64, len(reps))
	for i, r := range reps {
		vs[i] = r.Version
	}
	var kth int64
	for k := 0; k < d.Quorum; k++ {
		best := int64(-1)
		bestIdx := -1
		for i, v := range vs {
			if v > best {
				best, bestIdx = v, i
			}
		}
		kth = best
		vs[bestIdx] = 0
	}
	d.CommittedVersion = kth

	intact := []Replica{}
	for _, r := range reps {
		if r.SavedDigest == r.ActualDigest {
			intact = append(intact, r)
		}
	}
	if len(intact) == 0 {
		d.Outcome = OutcomeNoSource
		return d
	}
	maxV := intact[0].Version
	for _, r := range intact[1:] {
		if r.Version > maxV {
			maxV = r.Version
		}
	}
	if maxV < d.CommittedVersion {
		d.Outcome = OutcomeCommittedLost
		return d
	}
	dig := ""
	conflict := false
	for _, r := range intact {
		if r.Version != maxV {
			continue
		}
		if dig == "" {
			dig = r.SavedDigest
		} else if r.SavedDigest != dig {
			conflict = true
		}
	}
	if conflict {
		d.Outcome = OutcomeConflict
		return d
	}

	d.AuthorityVersion, d.AuthorityDigest = maxV, dig
	d.Outcome = OutcomeNoRepair
	for _, r := range reps {
		if r.SavedDigest != r.ActualDigest {
			d.Targets = append(d.Targets, Target{Node: r.Node, Bitrot: true})
		} else if r.Version < maxV {
			d.Targets = append(d.Targets, Target{Node: r.Node, Bitrot: false})
		}
	}
	if len(d.Targets) > 0 {
		d.Outcome = OutcomeRepaired
	}
	return d
}

func (m *Model) tickGuard(at Time) ErrorClass {
	if at < m.clock {
		return ErrClockBack
	}
	return 0
}

func (m *Model) CreateBlock(at Time, id int, reps []Replica, interval Time) ErrorClass {
	if interval < 0 || len(reps) < 2 || len(reps) > 5 {
		return ErrInvalid
	}
	nodes := map[int]bool{}
	for _, r := range reps {
		if r.Version <= 0 || r.SavedDigest == "" || nodes[r.Node] {
			return ErrInvalid
		}
		nodes[r.Node] = true
	}
	if e := m.tickGuard(at); e != 0 {
		return e
	}
	if _, exists := m.blocks[id]; exists {
		return ErrInvalid
	}
	b := &block{reps: map[int]Replica{}, interval: interval}
	for _, r := range reps {
		b.reps[r.Node] = r
	}
	m.blocks[id] = b
	m.clock = at
	return 0
}

func (m *Model) Write(at Time, id int, v int64, dig string, nodes []int) ErrorClass {
	if v <= 0 || dig == "" || len(nodes) == 0 {
		return ErrInvalid
	}
	seen := map[int]bool{}
	for _, n := range nodes {
		if seen[n] {
			return ErrInvalid
		}
		seen[n] = true
	}
	if e := m.tickGuard(at); e != 0 {
		return e
	}
	b := m.blocks[id]
	if b == nil {
		return ErrNotFound
	}
	for _, n := range nodes {
		b.reps[n] = Replica{Node: n, Version: v, SavedDigest: dig, ActualDigest: dig}
	}
	m.clock = at
	return 0
}

func (m *Model) Rot(at Time, id, node int, corrupt string) ErrorClass {
	if corrupt == "" {
		return ErrInvalid
	}
	if e := m.tickGuard(at); e != 0 {
		return e
	}
	b := m.blocks[id]
	if b == nil {
		return ErrNotFound
	}
	r, ok := b.reps[node]
	if !ok || corrupt == r.SavedDigest {
		return ErrInvalid
	}
	r.ActualDigest = corrupt
	b.reps[node] = r
	m.clock = at
	return 0
}

func (m *Model) Discard(at Time, id, node int) ErrorClass {
	if e := m.tickGuard(at); e != 0 {
		return e
	}
	b := m.blocks[id]
	if b == nil {
		return ErrNotFound
	}
	if _, ok := b.reps[node]; !ok {
		return ErrInvalid
	}
	delete(b.reps, node)
	if len(b.reps) == 0 {
		delete(m.blocks, id)
	}
	m.clock = at
	return 0
}

func (m *Model) Patrol(at Time, id int, fail map[int]bool) (PatrolResult, ErrorClass) {
	if e := m.tickGuard(at); e != 0 {
		return PatrolResult{}, e
	}
	b := m.blocks[id]
	if b == nil {
		return PatrolResult{}, ErrNotFound
	}
	if b.hasPatrol && at < b.last+b.interval {
		return PatrolResult{}, ErrTooFrequent
	}

	list := make([]Replica, 0, len(b.reps))
	for _, r := range b.reps {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Node < list[j].Node })
	d := judge(list)
	res := PatrolResult{Decision: d, Time: at}

	if d.Outcome == OutcomeRepaired {
		for _, t := range d.Targets {
			if fail[t.Node] {
				res.FailedNodes = append(res.FailedNodes, t.Node)
				continue
			}
			r := b.reps[t.Node]
			r.Version = d.AuthorityVersion
			r.SavedDigest = d.AuthorityDigest
			r.ActualDigest = d.AuthorityDigest
			b.reps[t.Node] = r
			res.Repaired = append(res.Repaired, t.Node)
		}
		if len(res.FailedNodes) > 0 {
			res.Outcome = OutcomePartial
		}
	}

	b.hasPatrol = true
	b.last = at
	if d.Outcome == OutcomeNoSource || d.Outcome == OutcomeCommittedLost || d.Outcome == OutcomeConflict {
		m.alerts = append(m.alerts, Alert{Seq: len(m.alerts) + 1, Time: at, BlockID: id, Outcome: d.Outcome})
	}
	m.clock = at
	return res, 0
}

// Due 朴素线性扫描全部块并排序。
func (m *Model) Due(at Time, limit int) ([]int, ErrorClass) {
	if limit <= 0 {
		return nil, ErrInvalid
	}
	if e := m.tickGuard(at); e != 0 {
		return nil, e
	}
	type item struct {
		id, last int
		never    bool
	}
	var items []item
	for id, b := range m.blocks {
		due := b.last + b.interval
		if !b.hasPatrol || at >= due {
			items = append(items, item{id: id, last: int(b.last), never: !b.hasPatrol})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].never != items[j].never {
			return items[i].never
		}
		if items[i].never {
			return items[i].id < items[j].id
		}
		if items[i].last != items[j].last {
			return items[i].last < items[j].last
		}
		return items[i].id < items[j].id
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]int, len(items))
	for i, x := range items {
		out[i] = x.id
	}
	return out, 0
}

func (m *Model) Alerts() []Alert {
	out := make([]Alert, len(m.alerts))
	copy(out, m.alerts)
	return out
}

// Replicas 返回某块按节点排序的副本快照，供对照测试比对状态。
func (m *Model) Replicas(id int) ([]Replica, bool) {
	b := m.blocks[id]
	if b == nil {
		return nil, false
	}
	out := make([]Replica, 0, len(b.reps))
	for _, r := range b.reps {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out, true
}

func (m *Model) Clock() Time { return m.clock }
