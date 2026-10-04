package route

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/numplan"
	"ontology/portdb"
)

type diffDeps struct {
	plan *numplan.Plan
	db   *portdb.DB
	r    *Router
}

func newRouterWith(lmin, q int64) diffDeps {
	plan := numplan.New()
	db := portdb.New(plan, lmin, q)
	return diffDeps{plan: plan, db: db, r: New(db)}
}

// 朴素模型：保存全部已接受事件，每个问题从完整事件线性重算。
type naive struct {
	lmin, q int64
	maxNow  int64
	blocks  []nb
	ports   []nport
	disc    []ndisc
	seq     int64
}

type nb struct {
	prefix string
	length int
	op     int
	eff    int64
}

type nport struct {
	num       string
	at        int64
	seq       int64
	recipient int
	id        int64
	canceled  bool
}

type ndisc struct {
	num string
	at  int64
}

func newNaive(lmin, q int64) *naive {
	return &naive{lmin: lmin, q: q}
}

func (m *naive) home(num string, t int64) (int, bool) {
	bestLen, bestOp := -1, 0
	for _, b := range m.blocks {
		if len(num) == b.length && strings.HasPrefix(num, b.prefix) && b.eff <= t && len(b.prefix) > bestLen {
			bestLen = len(b.prefix)
			bestOp = b.op
		}
	}
	if bestLen == -1 {
		return 0, false
	}
	return bestOp, true
}

func (m *naive) lastDisc(num string, t int64) int64 {
	d := int64(-1)
	for _, x := range m.disc {
		if x.num == num && x.at <= t && x.at > d {
			d = x.at
		}
	}
	return d
}

func (m *naive) state(num string, t int64) (home, serving int, ported, frozen bool, err error) {
	h, ok := m.home(num, t)
	if !ok {
		return 0, 0, false, false, numplan.ErrNotAssigned
	}
	s := h
	d := m.lastDisc(num, t)
	if d != -1 && t >= d && t < d+m.q {
		return h, h, false, true, nil
	}
	lastPort, lastSeq, recipient := int64(-1), int64(-1), 0
	for _, p0 := range m.ports {
		if p0.num == num && !p0.canceled && p0.at <= t &&
			(p0.at > lastPort || p0.at == lastPort && p0.seq > lastSeq) {
			lastPort, lastSeq, recipient = p0.at, p0.seq, p0.recipient
		}
	}
	if lastPort != -1 && (d == -1 || d < lastPort) {
		s = recipient
	}
	return h, s, s != h, false, nil
}

func (m *naive) pending(num string, now int64) bool {
	for _, p0 := range m.ports {
		if p0.num == num && !p0.canceled && p0.at > now {
			return true
		}
	}
	return false
}

func (m *naive) pathFor(h, s, orig int, method Method) []int {
	if s == orig {
		return []int{}
	}
	if method == ACQ {
		return []int{s}
	}
	if h == s || h == orig {
		return []int{s}
	}
	return []int{h, s}
}

func sameErr(want, got error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func errName(e error) string {
	if e == nil {
		return "nil"
	}
	return e.Error()
}

func methodName(m Method) string {
	if m == ACQ {
		return "ACQ"
	}
	return "OR"
}

type diffLog struct {
	text, want, got, why string
}

// TestRandomDifferential 以 1500 组随机操作序列对照朴素线性重算。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	const sequences, maxSteps = 1500, 30
	for seq := 0; seq < sequences; seq++ {
		runDiffSequence(t, rng, seq, maxSteps)
	}
}

func runDiffSequence(t *testing.T, rng *rand.Rand, seq, maxSteps int) {
	t.Helper()
	lmin := int64(rng.Intn(6))
	q := int64(rng.Intn(8))
	p := newRouterWith(lmin, q)
	r := p.r
	m := newNaive(lmin, q)
	must0(p.plan.AssignBlock("13", 5, 1, 0))
	m.blocks = append(m.blocks, nb{"13", 5, 1, 0})
	must0(p.plan.AssignBlock("138", 8, 2, 1))
	m.blocks = append(m.blocks, nb{"138", 8, 2, 1})
	m.maxNow = 1
	pool := []string{"13001", "13002", "13800001", "13800002"}
	nextID := int64(1)
	var logs []diffLog
	fail := false
	record := func(l diffLog) { logs = append(logs, l); fail = true }
	for step := 0; step < maxSteps && !fail; step++ {
		num := pool[rng.Intn(len(pool))]
		now := m.maxNow + int64(rng.Intn(4))
		switch rng.Intn(7) {
		case 0, 1:
			_, srv, _, frozen, serr := m.state(num, now)
			donor := srv
			if serr == nil && rng.Intn(3) == 0 {
				donor = 1 + rng.Intn(5)
			}
			if serr != nil {
				donor = 1 + rng.Intn(5)
			}
			recipient := 1 + rng.Intn(5)
			at := now + int64(rng.Intn(8))
			var want error
			switch {
			case now < m.maxNow:
				want = numplan.ErrClockBack
			case serr != nil:
				want = serr
			case frozen:
				want = numplan.ErrFrozen
			case m.pending(num, now):
				want = numplan.ErrPending
			case donor != srv:
				want = numplan.ErrDonor
			case recipient == donor:
				want = numplan.ErrSameOp
			case at-now < m.lmin:
				want = numplan.ErrLeadTime
			}
			id, got := r.DB().RequestPort(num, donor, recipient, at, now)
			if !sameErr(want, got) {
				record(diffLog{
					fmt.Sprintf("RequestPort(%s,donor=%d,rec=%d,at=%d,now=%d)", num, donor, recipient, at, now),
					errName(want), errName(got),
					fmt.Sprintf("srv=%d frozen=%v pending=%v lmin=%d maxNow=%d", srv, frozen, m.pending(num, now), m.lmin, m.maxNow),
				})
				break
			}
			if want == nil {
				if id != nextID {
					t.Fatalf("seq=%d id=%d want=%d", seq, id, nextID)
				}
				m.seq++
				m.ports = append(m.ports, nport{num, at, m.seq, recipient, id, false})
				nextID++
				m.maxNow = now
			}
		case 2:
			var order int64
			if nextID > 1 && rng.Intn(2) == 0 {
				order = 1 + rng.Int63n(nextID-1)
			} else {
				order = nextID + int64(rng.Intn(5))
			}
			var want error
			if now < m.maxNow {
				want = numplan.ErrClockBack
			} else {
				found := false
				for i := range m.ports {
					p0 := &m.ports[i]
					if p0.id != order {
						continue
					}
					found = true
					if p0.canceled {
						want = numplan.ErrNoOrder
					} else if now >= p0.at {
						want = numplan.ErrEffective
					} else {
						p0.canceled = true
					}
					break
				}
				if !found {
					want = numplan.ErrNoOrder
				}
			}
			got := r.DB().Cancel(order, now)
			if !sameErr(want, got) {
				record(diffLog{fmt.Sprintf("Cancel(order=%d,now=%d)", order, now), errName(want), errName(got),
					fmt.Sprintf("maxNow=%d", m.maxNow)})
				break
			}
			if want == nil {
				m.maxNow = now
			}
		case 3:
			_, _, _, frozen, serr := m.state(num, now)
			var want error
			switch {
			case now < m.maxNow:
				want = numplan.ErrClockBack
			case serr != nil:
				want = serr
			case frozen:
				want = numplan.ErrFrozen
			}
			got := r.DB().Disconnect(num, now)
			if !sameErr(want, got) {
				record(diffLog{fmt.Sprintf("Disconnect(%s,now=%d)", num, now), errName(want), errName(got),
					fmt.Sprintf("frozen=%v serr=%v maxNow=%d", frozen, serr, m.maxNow)})
				break
			}
			if want == nil {
				m.disc = append(m.disc, ndisc{num, now})
				for i := range m.ports {
					if m.ports[i].num == num && !m.ports[i].canceled && m.ports[i].at > now {
						m.ports[i].canceled = true
					}
				}
				m.maxNow = now
			}
		default:
			orig := 1 + rng.Intn(5)
			method := ACQ
			if rng.Intn(2) == 0 {
				method = OR
			}
			historical := rng.Intn(3) == 0
			qtime := now
			if historical {
				qtime = rng.Int63n(m.maxNow + 2)
			} else if now < m.maxNow {
				qtime = m.maxNow
			}
			h, s, ported, frozen, serr := m.state(num, qtime)
			var want error
			switch {
			case historical && qtime > m.maxNow:
				want = numplan.ErrInvalid
			case !historical && qtime < m.maxNow:
				want = numplan.ErrClockBack
			case serr != nil:
				want = serr
			case frozen:
				want = numplan.ErrFrozen
			}
			var got Result
			var gotErr error
			if historical {
				got, gotErr = r.QueryAt(num, orig, method, qtime)
			} else {
				got, gotErr = r.Query(num, orig, method, qtime)
			}
			if !sameErr(want, gotErr) {
				record(diffLog{
					fmt.Sprintf("Query(num=%s,orig=%d,%s,t=%d,hist=%v)", num, orig, methodName(method), qtime, historical),
					errName(want), errName(gotErr), fmt.Sprintf("maxNow=%d", m.maxNow),
				})
				break
			}
			if want == nil {
				wantPath := m.pathFor(h, s, orig, method)
				if got.Home != h || got.Serving != s || got.Ported != ported || !eqPath(got.Path, wantPath) {
					record(diffLog{
						fmt.Sprintf("Query(num=%s,orig=%d,%s,t=%d,hist=%v)", num, orig, methodName(method), qtime, historical),
						fmt.Sprintf("h=%d s=%d ported=%v path=%v", h, s, ported, wantPath),
						fmt.Sprintf("h=%d s=%d ported=%v path=%v", got.Home, got.Serving, got.Ported, got.Path),
						"state mismatch",
					})
					break
				}
				if !historical {
					m.maxNow = qtime
				}
			}
		}
	}
	if fail {
		for _, l := range logs {
			t.Errorf("seq=%d\n  IN  : %s\n  WANT: %s\n  GOT : %s\n  WHY : %s", seq, l.text, l.want, l.got, l.why)
		}
	} else {
		t.Logf("seq=%d OK: lmin=%d q=%d ports=%d discs=%d maxNow=%d",
			seq, lmin, q, len(m.ports), len(m.disc), m.maxNow)
	}
}
