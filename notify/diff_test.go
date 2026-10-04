package notify

import (
	"errors"
	"fmt"
	"sort"

	"ontology/alert"
)

// 随机对照测试：产品 Manager 与独立编写的朴素逐步模拟逐步比对；
// 日志打印每步输入、输出与判定依据（why）。

type opKind int

const (
	kResult opKind = iota
	kNotify
	kReadBack
	kAct
	kSetWard
	kGrant
	kAddTest
)

type op struct {
	kind                        opKind
	now                         int64
	patient, code, u1, u2, ward string
	event                       int64
	v, low, high, step          int64
	role                        Role
}

func (o op) String() string {
	switch o.kind {
	case kResult:
		return fmt.Sprintf("Result(now=%d %s %s v=%d)", o.now, o.patient, o.code, o.v)
	case kNotify:
		return fmt.Sprintf("Notify(now=%d e=%d tech=%s recv=%s)", o.now, o.event, o.u1, o.u2)
	case kReadBack:
		return fmt.Sprintf("ReadBack(now=%d e=%d recv=%s v=%d)", o.now, o.event, o.u2, o.v)
	case kAct:
		return fmt.Sprintf("Act(now=%d e=%d doc=%s)", o.now, o.event, o.u2)
	case kSetWard:
		return fmt.Sprintf("SetWard(%s,%s)", o.patient, o.ward)
	case kGrant:
		return fmt.Sprintf("Grant(%s,%s,%v)", o.u1, o.ward, o.role)
	case kAddTest:
		return fmt.Sprintf("AddTest(%s low=%d high=%d step=%d)", o.code, o.low, o.high, o.step)
	}
	return "?"
}

type nTest struct{ low, high, step int64 }

type nEvent struct {
	id             int64
	patient, code  string
	sev            int
	rep, deadline  int64
	state          alert.State
	late           bool
	receiver, tech string
	mismatch       int
	nresults       int
}

type naive struct {
	t       [4]int64
	maxNow  int64
	tests   map[string]nTest
	ward    map[string]string
	perm    map[string]map[grant]struct{}
	events  []*nEvent
	open    map[string]*nEvent
	overdue []int64
}

func newNaive(t1, t2, t3 int64) *naive {
	return &naive{
		t:     [4]int64{0, t1, t2, t3},
		tests: map[string]nTest{},
		ward:  map[string]string{},
		perm:  map[string]map[grant]struct{}{},
		open:  map[string]*nEvent{},
	}
}

func nkey(p, c string) string { return p + "\x00" + c }

func (n *naive) severity(code string, v int64) (int, bool) {
	t, ok := n.tests[code]
	if !ok {
		return 0, false
	}
	var x int64
	switch {
	case v <= t.low:
		x = t.low - v
	case v >= t.high:
		x = v - t.high
	default:
		return 0, false
	}
	s := int(1 + x/t.step)
	if s > 3 {
		s = 3
	}
	return s, true
}

type nOutcome struct {
	err                    error
	normal                 bool
	eid                    int64
	created, upgraded, mis bool
	sev                    int
	rep, deadline          int64
	state                  alert.State
	late                   bool
	land                   []int64
	why                    string
}

func (n *naive) hasRole(user, ward string, role Role) bool {
	g, ok := n.perm[user]
	if !ok {
		return false
	}
	_, ok = g[grant{ward: ward, role: role}]
	return ok
}

// land 在判定前按 (deadline,id) 升序落地全部新逾期事件（朴素 O(事件数) 扫描）。
func (n *naive) land(now int64) []int64 {
	type cand struct {
		id, d int64
		e     *nEvent
	}
	var cs []cand
	for _, e := range n.events {
		if e.state != alert.StateClosed && !e.late && e.deadline < now {
			cs = append(cs, cand{e.id, e.deadline, e})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].id < cs[j].id
	})
	var ids []int64
	for _, c := range cs {
		c.e.late = true
		delete(n.open, nkey(c.e.patient, c.e.code))
		n.overdue = append(n.overdue, c.e.id)
		ids = append(ids, c.e.id)
	}
	return ids
}

func (n *naive) ev(id int64) *nEvent {
	if id < 1 || int(id) > len(n.events) {
		return nil
	}
	return n.events[id-1]
}

func validV(v int64) bool { return v >= -1_000_000_000 && v <= 1_000_000_000 }

func errEq(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b)
}
