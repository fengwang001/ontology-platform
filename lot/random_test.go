package lot_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/lot"
	"ontology/plan"
)

// 朴素模型：保存全部初检历史，每次转移都对全量历史切片回看
type histItem struct {
	d       int
	verdict lot.Decision
}

type nStream struct {
	exists bool
	sev    plan.Severity
	hist   []histItem // 自进入当前严格度以来的全部初检（切换即清空）
	open   string
}

type nLot struct {
	id      string
	k       lot.StreamKey
	n       int
	scheme  plan.Scheme
	status  lot.Status
	atSev   plan.Severity
	recheck bool
}

type naive struct {
	tab     *plan.Table
	streams map[lot.StreamKey]*nStream
	lots    map[string]*nLot
}

func newNaive(tab *plan.Table) *naive {
	return &naive{tab: tab, streams: map[lot.StreamKey]*nStream{}, lots: map[string]*nLot{}}
}

func classify(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, lot.ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, lot.ErrUnauthorized):
		return "unauth"
	case errors.Is(err, lot.ErrNotFound):
		return "notfound"
	case errors.Is(err, lot.ErrSuspended):
		return "suspended"
	case errors.Is(err, lot.ErrConflictState):
		return "state"
	case errors.Is(err, lot.ErrConflictID):
		return "conflict"
	case errors.Is(err, lot.ErrCountExceedsN):
		return "d>n"
	}
	return "other:" + err.Error()
}

func verdictOf(d int, sc plan.Scheme) lot.Decision {
	switch {
	case d <= sc.Ac:
		return lot.Accept
	case d >= sc.Re:
		return lot.Reject
	default:
		return lot.BorderlineAccept
	}
}

// observe 在朴素模型上喂一条初检结果，返回“判定依据”描述。逐条回看全部历史。
func (m *naive) observe(s *nStream, d int, v lot.Decision) string {
	s.hist = append(s.hist, histItem{d, v})
	switch s.sev {
	case plan.Normal:
		h := s.hist
		rejects := 0
		start5 := len(h) - 5
		if start5 < 0 {
			start5 = 0
		}
		for i := start5; i < len(h); i++ {
			if h[i].verdict == lot.Reject {
				rejects++
			}
		}
		if rejects >= 2 {
			s.sev = plan.Tightened
			s.hist = nil
			return "normal: 2 rejects in last 5 -> tightened"
		}
		if len(h) >= 10 {
			sum, allAccept := 0, true
			for _, r := range h[len(h)-10:] {
				sum += r.d
				if r.verdict != lot.Accept {
					allAccept = false
				}
			}
			if allAccept && sum <= m.tab.LR() {
				s.sev = plan.Reduced
				s.hist = nil
				return fmt.Sprintf("normal: last10 all accept sum=%d<=Lr -> reduced", sum)
			}
		}
		return "normal: stay"
	case plan.Tightened:
		run, totalR := 0, 0
		for i := len(s.hist) - 1; i >= 0; i-- {
			if s.hist[i].verdict == lot.Accept {
				run++
			} else {
				break
			}
		}
		for _, r := range s.hist {
			if r.verdict == lot.Reject {
				totalR++
			}
		}
		if run >= 5 {
			s.sev = plan.Normal
			s.hist = nil
			return "tightened: 5 consecutive accepts -> normal"
		}
		if totalR >= 5 {
			s.sev = plan.Suspended
			s.hist = nil
			return "tightened: 5 cumulative rejects -> suspended"
		}
		return "tightened: stay"
	case plan.Reduced:
		if v != lot.Accept {
			s.sev = plan.Normal
			s.hist = nil
			if v == lot.BorderlineAccept {
				return "reduced: borderline -> normal"
			}
			return "reduced: reject -> normal"
		}
		return "reduced: stay"
	}
	return "suspended"
}

type opKind int

const (
	kSubmit opKind = iota
	kRecord
	kResubmit
	kResume
)

type op struct {
	kind opKind
	op   lot.Operator
	k    lot.StreamKey
	id   string
	n    int
	d    int
}

func (m *naive) submit(o op) (string, plan.Scheme) {
	if o.op.ID == "" || o.id == "" || o.k.Supplier == "" || o.k.Material == "" || o.n < 1 || o.n > 1_000_000 {
		return "invalid", plan.Scheme{}
	}
	if o.op.Role != lot.RoleInspector {
		return "unauth", plan.Scheme{}
	}
	s, known := m.streams[o.k]
	if known && s.sev == plan.Suspended {
		return "suspended", plan.Scheme{}
	}
	if known && s.open != "" {
		return "state", plan.Scheme{}
	}
	if _, dup := m.lots[o.id]; dup {
		return "conflict", plan.Scheme{}
	}
	if !known {
		s = &nStream{exists: true, sev: plan.Normal}
		m.streams[o.k] = s
	}
	sc, err := m.tab.SchemeFor(o.n, s.sev)
	if err != nil {
		if errors.Is(err, plan.ErrNoRange) {
			return "invalid", plan.Scheme{}
		}
		return classify(err), plan.Scheme{}
	}
	m.lots[o.id] = &nLot{id: o.id, k: o.k, n: o.n, scheme: sc, status: lot.Pending, atSev: s.sev}
	s.open = o.id
	return "ok", sc
}

func (m *naive) record(o op) (string, lot.Decision, lot.Status, string) {
	if o.op.ID == "" || o.id == "" || o.d < 0 {
		return "invalid", 0, 0, ""
	}
	if o.op.Role != lot.RoleInspector {
		return "unauth", 0, 0, ""
	}
	l, ok := m.lots[o.id]
	if !ok {
		return "notfound", 0, 0, ""
	}
	s := m.streams[l.k]
	if !l.recheck && s.sev == plan.Suspended {
		return "suspended", 0, 0, ""
	}
	if s.open != o.id || l.status != lot.Pending {
		return "state", 0, 0, ""
	}
	if o.d > l.scheme.N {
		return "d>n", 0, 0, ""
	}
	v := verdictOf(o.d, l.scheme)
	reason := ""
	if l.recheck {
		if v == lot.Reject {
			l.status = lot.Scrapped
			reason = "reinspection: d>=Re -> scrapped"
		} else {
			l.status = lot.Released
			reason = "reinspection: accepted -> released (no switch)"
		}
	} else {
		if v == lot.Reject {
			l.status = lot.Rejected
		} else {
			l.status = lot.Released
		}
		reason = m.observe(s, o.d, v)
	}
	s.open = ""
	return "ok", v, l.status, reason
}

func (m *naive) resubmit(o op) string {
	if o.op.ID == "" || o.id == "" {
		return "invalid"
	}
	if o.op.Role != lot.RoleInspector {
		return "unauth"
	}
	l, ok := m.lots[o.id]
	if !ok {
		return "notfound"
	}
	s := m.streams[l.k]
	if l.status != lot.Rejected || l.recheck {
		return "state"
	}
	if s.open != "" {
		return "state"
	}
	sc, err := m.tab.SchemeFor(l.n, plan.Tightened)
	if err != nil {
		return classify(err)
	}
	l.recheck = true
	l.status = lot.Pending
	l.scheme = sc
	s.open = l.id
	return "ok"
}

func (m *naive) resume(o op) string {
	if o.op.ID == "" || o.k.Supplier == "" || o.k.Material == "" {
		return "invalid"
	}
	if o.op.Role != lot.RoleManager {
		return "unauth"
	}
	s, ok := m.streams[o.k]
	if !ok || !s.exists {
		return "notfound"
	}
	if s.sev != plan.Suspended {
		return "state"
	}
	s.sev = plan.Tightened
	s.hist = nil
	s.open = ""
	return "ok"
}

var streamChoices = []lot.StreamKey{
	{Supplier: "sup1", Material: "m1"},
	{Supplier: "sup1", Material: "m2"},
	{Supplier: "sup2", Material: "m1"},
}

func TestAgainstNaive1500(t *testing.T) {
	tab := exampleTable(t)
	for seed := int64(0); seed < 1500; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			in := lot.NewInspector(tab)
			nm := newNaive(tab)
			var log []string
			failf := func(format string, args ...any) {
				for _, line := range log {
					t.Logf("%s", line)
				}
				t.Fatalf(format, args...)
			}
			nextID := 0
			mkID := func() string {
				nextID++
				return fmt.Sprintf("lot-%d", nextID)
			}
			var pendingIDs []string
			rejectedIDs := []string{}

			steps := 26
			for step := 0; step < steps; step++ {
				o := op{k: streamChoices[rng.Intn(len(streamChoices))], op: insp}
				badArg := rng.Intn(20) == 0
				kind := opKind(rng.Intn(4))
				switch kind {
				case kSubmit:
					o.kind = kSubmit
					o.id = mkID()
					o.n = []int{10, 60, 200, 5, 500}[rng.Intn(5)] // 5 与 500 故意无区间
					if badArg {
						switch rng.Intn(3) {
						case 0:
							o.id = ""
						case 1:
							o.n = 0
						case 2:
							o.op = lot.Operator{ID: "X", Role: lot.RoleManager}
						}
					}
					sc1, err1 := in.Submit(o.op, o.k, o.id, o.n)
					cls2, sc2 := nm.submit(o)
					if classify(err1) != cls2 {
						failf("step %d submit %+v: real=%s naive=%s", step, o, classify(err1), cls2)
					}
					if err1 == nil {
						if sc1.Scheme != sc2 {
							failf("step %d submit scheme: real=%+v naive=%+v", step, sc1.Scheme, sc2)
						}
						pendingIDs = append(pendingIDs, o.id)
					}
					t.Logf("seed=%d step %d Submit(k=%v id=%q n=%d) -> %s scheme=%+v", seed, step, o.k, o.id, o.n, classify(err1), sc1)
					log = append(log, fmt.Sprintf("step %d Submit(id=%q n=%d) -> %s scheme=%v", step, o.id, o.n, classify(err1), sc1))
				case kRecord:
					o.kind = kRecord
					pool := append(append([]string{}, pendingIDs...), rejectedIDs...)
					pool = append(pool, "ghost")
					o.id = pool[rng.Intn(len(pool))]
					// d 范围覆盖边界与边缘（Reduced 档 0<d<2）
					o.d = []int{0, 1, 2, 3, 5, 50, 60, -1}[rng.Intn(8)]
					if badArg {
						o.d = -1
					}
					dec1, st1, err1 := in.Record(o.op, o.id, o.d)
					cls2, dec2, st2, reason := nm.record(o)
					if classify(err1) != cls2 || dec1 != dec2 || st1 != st2 {
						failf("step %d Record(id=%q d=%d): real=(%s,%v,%v) naive=(%s,%v,%v)",
							step, o.id, o.d, classify(err1), dec1, st1, cls2, dec2, st2)
					}
					if err1 == nil {
						pendingIDs = removeStr(pendingIDs, o.id)
						if st1 == lot.Rejected {
							rejectedIDs = append(rejectedIDs, o.id)
						} else {
							rejectedIDs = removeStr(rejectedIDs, o.id)
						}
						rl, _ := in.Lookup(o.id)
						nl := nm.lots[o.id]
						if rl.Status != nl.status || rl.Scheme != nl.scheme || rl.Reinspection != nl.recheck {
							failf("step %d lot divergence id=%s: real=%+v naive=%+v", step, o.id, rl, nl)
						}
					}
					t.Logf("seed=%d step %d Record(id=%q d=%d) -> %s decision=%v status=%v | %s", seed, step, o.id, o.d, classify(err1), dec1, st1, reason)
					log = append(log, fmt.Sprintf("step %d Record(id=%q d=%d) -> %s decision=%v status=%v | %s", step, o.id, o.d, classify(err1), dec1, st1, reason))
				case kResubmit:
					o.kind = kResubmit
					pool := append(append([]string{}, rejectedIDs...), pendingIDs...)
					pool = append(pool, "ghost")
					o.id = pool[rng.Intn(len(pool))]
					if badArg {
						o.op = lot.Operator{ID: "", Role: lot.RoleInspector}
					}
					err1 := in.Resubmit(o.op, o.id)
					cls2 := nm.resubmit(o)
					if classify(err1) != cls2 {
						failf("step %d Resubmit(id=%q): real=%s naive=%s", step, o.id, classify(err1), cls2)
					}
					if err1 == nil {
						rejectedIDs = removeStr(rejectedIDs, o.id)
						pendingIDs = append(pendingIDs, o.id)
					}
					t.Logf("seed=%d step %d Resubmit(id=%q) -> %s", seed, step, o.id, classify(err1))
					log = append(log, fmt.Sprintf("step %d Resubmit(id=%q) -> %s", step, o.id, classify(err1)))
				case kResume:
					o.kind = kResume
					if badArg {
						o.op = insp // Inspector 无权限
					} else {
						o.op = mgr
					}
					err1 := in.Resume(o.op, o.k)
					cls2 := nm.resume(o)
					if classify(err1) != cls2 {
						failf("step %d Resume(%v): real=%s naive=%s", step, o.k, classify(err1), cls2)
					}
					t.Logf("seed=%d step %d Resume(k=%v role=%v) -> %s", seed, step, o.k, o.op.Role, classify(err1))
					log = append(log, fmt.Sprintf("step %d Resume(%v role=%v) -> %s", step, o.k, o.op.Role, classify(err1)))
				}
			}

			// 终态：所有流严格度与所有批快照一致
			for k, ns := range nm.streams {
				sev, ok := in.Severity(k)
				if !ok || sev != ns.sev {
					failf("final severity %v: real=%v(%v) naive=%v", k, sev, ok, ns.sev)
				}
			}
			for id, nl := range nm.lots {
				rl, ok := in.Lookup(id)
				if !ok || rl.Status != nl.status || rl.Scheme != nl.scheme ||
					rl.Reinspection != nl.recheck || rl.AtSeverity != nl.atSev {
					failf("final lot %s: real=%+v ok=%v naive=%+v", id, rl, ok, nl)
				}
			}
		})
	}
}

func removeStr(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
