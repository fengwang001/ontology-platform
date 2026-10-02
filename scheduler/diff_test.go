package scheduler

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// mTask / mNode 是严格按题目规则逐步写成的朴素参考模型。
type mTask struct {
	id, prio, w, iv, ck int64
	p, cp, rs           int64
	st                  TaskState
	node                int64
}

type mNode struct {
	id       int64
	kind     NodeKind
	slots    int64
	used     int64
	notified bool
	dl       int64
}

type refModel struct {
	g, pod, bud int64
	nodes       map[int64]*mNode
	tasks       map[int64]*mTask
}

func newRefModel(g, pod, bud int64) *refModel {
	return &refModel{g: g, pod: pod, bud: bud,
		nodes: map[int64]*mNode{}, tasks: map[int64]*mTask{}}
}

// op 描述一个操作；字段含义随 kind 变化，见生成器。
type op struct {
	kind string
	id   int64
	p    int64
	w    int64
	iv   int64
	ck   int64
}

func (m *refModel) addNode(o op) string {
	kind := NodeKind(o.iv)
	if o.id < 1 || (kind != Spot && kind != OnDemand) || o.w < 1 || o.w > 1000 {
		return errCode(ErrInvalidArgument)
	}
	if _, ok := m.nodes[o.id]; ok {
		return errCode(ErrExists)
	}
	m.nodes[o.id] = &mNode{id: o.id, kind: kind, slots: o.w}
	return "ok"
}

func (m *refModel) addTask(o op) string {
	if o.id < 1 || o.p < 0 || o.p > 255 ||
		o.w < 1 || o.w > 1_000_000_000 ||
		o.iv < 1 || o.iv > 1_000_000_000 ||
		o.ck < 1 || o.ck > 1_000_000_000 {
		return errCode(ErrInvalidArgument)
	}
	if _, ok := m.tasks[o.id]; ok {
		return errCode(ErrExists)
	}
	m.tasks[o.id] = &mTask{id: o.id, prio: o.p, w: o.w, iv: o.iv, ck: o.ck, st: Pending}
	return "ok"
}

func (m *refModel) place() {
	var pend []*mTask
	for _, t := range m.tasks {
		if t.st == Pending {
			pend = append(pend, t)
		}
	}
	sort.Slice(pend, func(i, j int) bool {
		if pend[i].prio != pend[j].prio {
			return pend[i].prio > pend[j].prio
		}
		return pend[i].id < pend[j].id
	})
	for _, t := range pend {
		var target *mNode
		if t.rs < 2 {
			for _, n := range m.nodes {
				if n.notified || n.kind != Spot || n.used >= n.slots {
					continue
				}
				if target == nil || n.slots-n.used > target.slots-target.used ||
					(n.slots-n.used == target.slots-target.used && n.id < target.id) {
					target = n
				}
			}
		}
		if target == nil {
			var od *mNode
			for _, n := range m.nodes {
				if n.kind != OnDemand || n.used >= n.slots {
					continue
				}
				if od == nil || n.id < od.id {
					od = n
				}
			}
			if od != nil && (t.w-t.cp)*m.pod <= m.bud {
				m.bud -= (t.w - t.cp) * m.pod
				target = od
			}
		}
		if target != nil {
			target.used++
			t.st = Running
			t.node = target.id
		}
	}
}

func (m *refModel) report(o op) string {
	t, ok := m.tasks[o.id]
	if !ok {
		return errCode(ErrNotFound)
	}
	if t.st != Running {
		return errCode(ErrNotRunning)
	}
	if o.p < t.p || o.p > t.w {
		return errCode(ErrInvalidArgument)
	}
	t.p = o.p
	if cp := o.p / t.iv * t.iv; cp > t.cp {
		t.cp = cp
	}
	if o.p == t.w {
		m.nodes[t.node].used--
		t.st = Done
		t.node = 0
	}
	return "ok"
}

func (m *refModel) notice(o op) (string, NoticeResult) {
	if o.p < 0 || o.p > 1_000_000_000_000_000_000 {
		return errCode(ErrInvalidArgument), NoticeResult{}
	}
	n, ok := m.nodes[o.id]
	if !ok {
		return errCode(ErrNotFound), NoticeResult{}
	}
	if n.kind != Spot {
		return errCode(ErrNotSpot), NoticeResult{}
	}
	if n.notified {
		return errCode(ErrNotified), NoticeResult{}
	}
	n.notified = true
	n.dl = o.p + m.g
	res := NoticeResult{}
	var need []*mTask
	for _, t := range m.tasks {
		if t.st != Running || t.node != n.id {
			continue
		}
		if t.w-t.p <= m.g {
			res.Completed = append(res.Completed, t.id)
		} else if t.p-t.cp == 0 {
			res.Saved = append(res.Saved, t.id)
		} else {
			need = append(need, t)
		}
	}
	sort.Slice(need, func(i, j int) bool {
		if need[i].p-need[i].cp != need[j].p-need[j].cp {
			return need[i].p-need[i].cp > need[j].p-need[j].cp
		}
		return need[i].id < need[j].id
	})
	var spent int64
	stop := false
	for _, t := range need {
		if !stop && spent+t.ck <= m.g {
			spent += t.ck
			t.cp = t.p
			res.Saved = append(res.Saved, t.id)
		} else {
			stop = true
			res.Discarded = append(res.Discarded, t.id)
		}
	}
	sortInt64(res.Completed)
	sortInt64(res.Saved)
	sortInt64(res.Discarded)
	return "ok", res
}

func (m *refModel) expire(o op) (string, []ExpireItem) {
	if o.p < 0 || o.p > 1_000_000_000_000_000_000 {
		return errCode(ErrInvalidArgument), nil
	}
	n, ok := m.nodes[o.id]
	if !ok {
		return errCode(ErrNotFound), nil
	}
	if !n.notified {
		return errCode(ErrNotNotified), nil
	}
	if o.p < n.dl {
		return errCode(ErrNotDue), nil
	}
	var items []ExpireItem
	for _, t := range m.tasks {
		if t.st != Running || t.node != n.id {
			continue
		}
		items = append(items, ExpireItem{TaskID: t.id, Rework: t.p - t.cp})
		t.rs++
		t.p = t.cp
		t.st = Pending
		t.node = 0
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TaskID < items[j].TaskID })
	delete(m.nodes, n.id)
	return "ok", items
}

func sortInt64(x []int64) { sort.Slice(x, func(i, j int) bool { return x[i] < x[j] }) }

var _ = fmt.Sprintf
var _ = strings.Builder{}
var _ = rand.Int

func errCode(e error) string {
	switch e {
	case nil:
		return "ok"
	case ErrInvalidConfig:
		return "InvalidConfig"
	case ErrInvalidArgument:
		return "InvalidArgument"
	case ErrExists:
		return "Exists"
	case ErrNotFound:
		return "NotFound"
	case ErrNotSpot:
		return "NotSpot"
	case ErrNotified:
		return "Notified"
	case ErrNotNotified:
		return "NotNotified"
	case ErrNotDue:
		return "NotDue"
	case ErrNotRunning:
		return "NotRunning"
	default:
		return e.Error()
	}
}

func runReal(s *Scheduler, o op) (string, NoticeResult, []ExpireItem) {
	switch o.kind {
	case "AddNode":
		return errCode(s.AddNode(o.id, NodeKind(o.iv), o.w)), NoticeResult{}, nil
	case "AddTask":
		return errCode(s.AddTask(o.id, o.p, o.w, o.iv, o.ck)), NoticeResult{}, nil
	case "Place":
		s.Place()
		return "ok", NoticeResult{}, nil
	case "Report":
		return errCode(s.Report(o.id, o.p)), NoticeResult{}, nil
	case "Notice":
		r, e := s.Notice(o.id, o.p)
		return errCode(e), r, nil
	case "Expire":
		it, e := s.Expire(o.id, o.p)
		return errCode(e), NoticeResult{}, it
	}
	return "?", NoticeResult{}, nil
}

type snapshot struct {
	bud   int64
	nodes map[string]string
	tasks map[string]string
}

func realSnapshot(s *Scheduler) snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := snapshot{bud: s.bud, nodes: map[string]string{}, tasks: map[string]string{}}
	for id, n := range s.nodes {
		sn.nodes[fmt.Sprint(id)] = fmt.Sprintf(
			"k=%d slots=%d used=%d not=%v dl=%d", n.Kind, n.Slots, n.used, n.notified, n.dl)
	}
	for id, t := range s.tasks {
		sn.tasks[fmt.Sprint(id)] = fmt.Sprintf(
			"prio=%d w=%d iv=%d ck=%d p=%d cp=%d rs=%d st=%d node=%d",
			t.Prio, t.W, t.Iv, t.Ck, t.p, t.cp, t.rs, t.st, t.node)
	}
	return sn
}

func modelSnapshot(m *refModel) snapshot {
	sn := snapshot{bud: m.bud, nodes: map[string]string{}, tasks: map[string]string{}}
	for id, n := range m.nodes {
		sn.nodes[fmt.Sprint(id)] = fmt.Sprintf(
			"k=%d slots=%d used=%d not=%v dl=%d", n.kind, n.slots, n.used, n.notified, n.dl)
	}
	for id, t := range m.tasks {
		sn.tasks[fmt.Sprint(id)] = fmt.Sprintf(
			"prio=%d w=%d iv=%d ck=%d p=%d cp=%d rs=%d st=%d node=%d",
			t.prio, t.w, t.iv, t.ck, t.p, t.cp, t.rs, t.st, t.node)
	}
	return sn
}

// TestRandomDifferential 与朴素参考模型对照 2000 组随机节点、任务与事件序列。
func TestRandomDifferential(t *testing.T) {
	const runs, opsN = 2000, 120
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < runs; iter++ {
		g := int64(rng.Intn(50) + 1)
		pod := int64(rng.Intn(5) + 1)
		bud := int64(rng.Intn(3000))
		s, _ := New(g, pod, bud)
		m := newRefModel(g, pod, bud)
		var log strings.Builder
		fmt.Fprintf(&log, "iter=%d New(G=%d,Pod=%d,Bud=%d)\n", iter, g, pod, bud)

		nodeSeq, taskSeq := int64(1), int64(1)
		liveNode := func() (int64, bool) {
			ids := make([]int64, 0, len(m.nodes))
			for id := range m.nodes {
				ids = append(ids, id)
			}
			if len(ids) == 0 {
				return 0, false
			}
			return ids[rng.Intn(len(ids))], true
		}
		liveTask := func() (int64, bool) {
			ids := make([]int64, 0, len(m.tasks))
			for id := range m.tasks {
				ids = append(ids, id)
			}
			if len(ids) == 0 {
				return 0, false
			}
			return ids[rng.Intn(len(ids))], true
		}

		for step := 0; step < opsN; step++ {
			o := op{}
			switch rng.Intn(10) {
			case 0, 1:
				o.kind = "AddNode"
				if rng.Intn(6) == 0 {
					if id, ok := liveNode(); ok {
						o.id = id
					} else {
						o.id = nodeSeq
						nodeSeq++
					}
				} else {
					o.id = nodeSeq
					nodeSeq++
				}
				o.iv = int64(rng.Intn(2))
				if rng.Intn(10) == 0 {
					o.w = int64(rng.Intn(1002))
				} else {
					o.w = int64(rng.Intn(4) + 1)
				}
			case 2, 3:
				o.kind = "AddTask"
				if rng.Intn(6) == 0 {
					if id, ok := liveTask(); ok {
						o.id = id
					} else {
						o.id = taskSeq
						taskSeq++
					}
				} else {
					o.id = taskSeq
					taskSeq++
				}
				o.p = int64(rng.Intn(258) - 1)
				o.w = int64(rng.Intn(80) + 1)
				o.iv = int64(rng.Intn(20) + 1)
				o.ck = int64(rng.Intn(20) + 1)
			case 4:
				o.kind = "Place"
			case 5, 6:
				o.kind = "Report"
				id, ok := liveTask()
				if !ok || rng.Intn(10) == 0 {
					o.id = 999
					o.p = 0
				} else {
					mt := m.tasks[id]
					o.id = mt.id
					if mt.st == Running {
						o.p = mt.p + int64(rng.Int63n(mt.w-mt.p+2))
					} else {
						o.p = int64(rng.Intn(50))
					}
				}
			case 7:
				o.kind = "Notice"
				id, ok := liveNode()
				if !ok || rng.Intn(8) == 0 {
					o.id = int64(rng.Intn(20)) + 1
				} else {
					o.id = id
				}
				if rng.Intn(12) == 0 {
					o.p = -1
				} else {
					o.p = int64(rng.Intn(200))
				}
			default:
				o.kind = "Expire"
				id, ok := liveNode()
				if !ok || rng.Intn(8) == 0 {
					o.id = int64(rng.Intn(20)) + 1
				} else {
					o.id = id
				}
				if rng.Intn(12) == 0 {
					o.p = -1
				} else {
					o.p = int64(rng.Intn(200))
				}
			}

			var mCode string
			var mRes NoticeResult
			var mItems []ExpireItem
			switch o.kind {
			case "AddNode":
				mCode = m.addNode(o)
			case "AddTask":
				mCode = m.addTask(o)
			case "Place":
				m.place()
				mCode = "ok"
			case "Report":
				mCode = m.report(o)
			case "Notice":
				mCode, mRes = m.notice(o)
			case "Expire":
				mCode, mItems = m.expire(o)
			}
			rCode, rRes, rItems := runReal(s, o)

			desc := fmt.Sprintf("%s(id=%d,p=%d,w=%d,iv=%d,ck=%d)",
				o.kind, o.id, o.p, o.w, o.iv, o.ck)
			basis := ""
			if o.kind == "Notice" && mCode == "ok" {
				basis = fmt.Sprintf(" -> comp=%v save=%v disc=%v",
					mRes.Completed, mRes.Saved, mRes.Discarded)
			}
			if o.kind == "Expire" && mCode == "ok" {
				basis = fmt.Sprintf(" -> items=%v", mItems)
			}
			fmt.Fprintf(&log, "%-45s model=%s real=%s%s\n", desc, mCode, rCode, basis)

			fail := mCode != rCode
			if o.kind == "Notice" && mCode == "ok" {
				if !sameInts(mRes.Completed, rRes.Completed) ||
					!sameInts(mRes.Saved, rRes.Saved) ||
					!sameInts(mRes.Discarded, rRes.Discarded) {
					fail = true
				}
			}
			if o.kind == "Expire" && mCode == "ok" {
				if len(mItems) != len(rItems) {
					fail = true
				} else {
					for i := range mItems {
						if mItems[i] != rItems[i] {
							fail = true
						}
					}
				}
			}
			if !sameSnap(modelSnapshot(m), realSnapshot(s)) {
				fail = true
			}
			if fail {
				t.Fatalf("iter %d step %d mismatch\nINPUT:\n%s\nJUDGE: model=%s real=%s\nmodel snap=%v\nreal snap=%v",
					iter, step, log.String(), mCode, rCode,
					modelSnapshot(m), realSnapshot(s))
			}
		}
		if iter < 5 {
			t.Logf("iter %d trace:\n%s", iter, log.String())
		}
	}
}

func sameInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameSnap(a, b snapshot) bool {
	if a.bud != b.bud || len(a.nodes) != len(b.nodes) || len(a.tasks) != len(b.tasks) {
		return false
	}
	for k, v := range a.nodes {
		if b.nodes[k] != v {
			return false
		}
	}
	for k, v := range a.tasks {
		if b.tasks[k] != v {
			return false
		}
	}
	return true
}
