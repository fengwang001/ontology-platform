package picker

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// ---------- 朴素模拟：严格按需求文字逐步写成，与产品代码相互独立 ----------

type simEp struct {
	id        string
	est, last int64
	has       bool
	inflight  int
	draining  bool
}

type sim struct {
	tau, p0, pf int64
	m, nmax     int
	eps         map[string]*simEp
	nextTicket  uint64
	tickets     map[uint64]*simEp
	maxNow      int64
	p2c         bool // false 时退化为"取首个候选"，用于均衡性对照
}

func newSim(tau, p0, pf int64, m, nmax int, p2c bool) *sim {
	return &sim{tau: tau, p0: p0, pf: pf, m: m, nmax: nmax,
		eps: make(map[string]*simEp), tickets: make(map[uint64]*simEp), p2c: p2c}
}

func simVal(e *simEp, now, tau, p0 int64) int64 {
	if !e.has {
		return p0
	}
	d := now - e.last
	if d > tau {
		d = tau
	}
	return e.est * (tau - d) / tau
}

func (s *sim) checkNow(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	return nil
}

func (s *sim) eligible() []*simEp {
	var elig []*simEp
	for _, e := range s.eps {
		if !e.draining && e.inflight < s.m {
			elig = append(elig, e)
		}
	}
	sort.Slice(elig, func(i, j int) bool { return elig[i].id < elig[j].id })
	return elig
}

func (s *sim) pick(now int64, r1, r2 uint64) (uint64, string, error) {
	if err := s.checkNow(now); err != nil {
		return 0, "", err
	}
	elig := s.eligible()
	n := len(elig)
	if n == 0 {
		for _, e := range s.eps {
			if !e.draining {
				return 0, "", ErrSaturated
			}
		}
		return 0, "", ErrNoEndpoints
	}
	win := elig[0]
	if n > 1 {
		i := int(r1 % uint64(n))
		if !s.p2c {
			win = elig[i] // 基线：取首个候选，不比较代价
		} else {
			j := (i + 1 + int(r2%uint64(n-1))) % n
			a, b := elig[i], elig[j]
			ca := simVal(a, now, s.tau, s.p0) * int64(a.inflight+1)
			cb := simVal(b, now, s.tau, s.p0) * int64(b.inflight+1)
			switch {
			case ca < cb:
				win = a
			case cb < ca:
				win = b
			case a.id < b.id:
				win = a
			default:
				win = b
			}
		}
	}
	win.inflight++
	s.nextTicket++
	s.tickets[s.nextTicket] = win
	s.maxNow = now
	return s.nextTicket, win.id, nil
}

func (s *sim) release(ticket uint64, rtt int64, ok bool, now int64) error {
	if rtt < 0 || rtt > 1_000_000_000 {
		return ErrInvalidRTT
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	ep, found := s.tickets[ticket]
	if !found {
		return ErrTicketUnknown
	}
	delete(s.tickets, ticket)
	sample := rtt
	if !ok {
		sample = s.pf
	}
	if !ep.has {
		ep.est = sample
	} else if v := simVal(ep, now, s.tau, 0); sample > v {
		ep.est = sample
	} else {
		ep.est = v
	}
	ep.last = now
	ep.has = true
	ep.inflight--
	if ep.draining && ep.inflight == 0 {
		delete(s.eps, ep.id)
	}
	s.maxNow = now
	return nil
}

func (s *sim) add(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	if _, dup := s.eps[id]; dup {
		return ErrExists
	}
	if len(s.eps) >= s.nmax {
		return ErrFull
	}
	s.eps[id] = &simEp{id: id}
	return nil
}

func (s *sim) remove(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	ep, found := s.eps[id]
	if !found {
		return ErrNotFound
	}
	if ep.inflight == 0 {
		delete(s.eps, id)
		return nil
	}
	if ep.draining {
		return ErrDraining
	}
	ep.draining = true
	return nil
}

// ---------- 随机操作序列 ----------

type opKind int

const (
	opAdd opKind = iota
	opRemove
	opPick
	opRelease
)

type op struct {
	kind     opKind
	id       string
	now, rtt int64
	r1, r2   uint64
	ticket   uint64
	ok       bool
}

func (o op) String() string {
	switch o.kind {
	case opAdd:
		return fmt.Sprintf("AddEndpoint(%q)", o.id)
	case opRemove:
		return fmt.Sprintf("RemoveEndpoint(%q)", o.id)
	case opPick:
		return fmt.Sprintf("Pick(now=%d, r1=%d, r2=%d)", o.now, o.r1, o.r2)
	default:
		return fmt.Sprintf("Release(ticket=%d, rtt=%d, ok=%v, now=%d)", o.ticket, o.rtt, o.ok, o.now)
	}
}

var simIDs = []string{"a", "b", "c", "dd", "e", "f0"}

// genOps 生成一条随机操作序列：now 多数单调推进，少数回退或越界；
// rtt 多数合法，少数越界；票据号覆盖未知与重复归还。
func genOps(rng *rand.Rand, n int) []op {
	ops := make([]op, 0, n)
	var curNow, maxTicket int64
	for k := 0; k < n; k++ {
		// 推进时间（供 pick/release 使用）。
		now := curNow + rng.Int63n(50)
		switch rng.Intn(20) {
		case 0:
			now = curNow - rng.Int63n(10) // 可能时钟回退
		case 1:
			now = -1 // 非法时间
		case 2:
			now = 1_000_000_000_000_001 // 非法时间
		}
		switch rng.Intn(10) {
		case 0, 1, 2: // Add
			id := simIDs[rng.Intn(len(simIDs))]
			if rng.Intn(15) == 0 {
				id = ""
			}
			ops = append(ops, op{kind: opAdd, id: id})
		case 3, 4: // Remove
			id := simIDs[rng.Intn(len(simIDs))]
			if rng.Intn(20) == 0 {
				id = "nosuch"
			}
			ops = append(ops, op{kind: opRemove, id: id})
		case 5, 6, 7, 8: // Pick
			ops = append(ops, op{kind: opPick, now: now, r1: rng.Uint64(), r2: rng.Uint64()})
			maxTicket++ // 乐观估计已发票据上界，使 Release 覆盖真实票据号
		default: // Release
			rtt := rng.Int63n(300)
			if rng.Intn(15) == 0 {
				rtt = []int64{-1, 1_000_000_001}[rng.Intn(2)]
			}
			tk := uint64(0)
			if maxTicket > 0 {
				tk = uint64(rng.Int63n(maxTicket + 2)) // 含未知与已归还
			}
			ops = append(ops, op{kind: opRelease, ticket: tk, rtt: rtt,
				ok: rng.Intn(4) != 0, now: now})
		}
		if now > curNow && now <= 1_000_000_000_000_000 {
			curNow = now
		}
	}
	return ops
}

// TestSimAgainstNaive 2000 组随机序列与朴素模拟逐步对照。
func TestSimAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		tau := 1 + rng.Int63n(200)
		p0 := 1 + rng.Int63n(100)
		pf := 1 + rng.Int63n(2000)
		m := 1 + rng.Intn(4)
		nmax := 1 + rng.Intn(8)

		sel, err := New(tau, p0, pf, m, nmax)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		ref := newSim(tau, p0, pf, m, nmax, true)
		ops := genOps(rng, 60)

		var nPickOK, nRelOK, nReject int
		for step, o := range ops {
			var gErr, wErr error
			var gTk, wTk uint64
			var gID, wID string
			switch o.kind {
			case opAdd:
				gErr, wErr = sel.AddEndpoint(o.id), ref.add(o.id)
			case opRemove:
				gErr, wErr = sel.RemoveEndpoint(o.id), ref.remove(o.id)
			case opPick:
				gTk, gID, gErr = sel.Pick(o.now, o.r1, o.r2)
				wTk, wID, wErr = ref.pick(o.now, o.r1, o.r2)
				if gErr == nil {
					nPickOK++
				}
			case opRelease:
				gErr, wErr = sel.Release(o.ticket, o.rtt, o.ok, o.now), ref.release(o.ticket, o.rtt, o.ok, o.now)
				if gErr == nil {
					nRelOK++
				}
			}
			if gErr != wErr || gTk != wTk || gID != wID {
				t.Fatalf("seq %d step %d 不一致\n输入: %s\n实现: ticket=%d id=%q err=%v\n模拟: ticket=%d id=%q err=%v\n判定: 同序重放结果必须完全相同",
					seq, step, o, gTk, gID, gErr, wTk, wID, wErr)
			}
			if gErr != nil {
				nReject++
			}
			checkInvariants(t, sel, ref, seq, step, o)
		}
		compareFinalState(t, sel, ref, seq)
		if seq < 3 || seq == sequences-1 {
			t.Logf("seq %d: tau=%d p0=%d pf=%d m=%d nmax=%d ops=%d 成功Pick=%d 成功Release=%d 被拒=%d 判定=逐步输出与朴素模拟一致",
				seq, tau, p0, pf, m, nmax, len(ops), nPickOK, nRelOK, nReject)
		}
	}
}

// checkInvariants 校验跨包不变量：Σ在途 == 未归还票据数、在途 ≤ M、
// 排空端点绝不在候选集中。
func checkInvariants(t *testing.T, sel *Selector, ref *sim, seq, step int, o op) {
	t.Helper()
	sum := 0
	for _, id := range ref.sortedIDs() {
		ep := sel.pl.Get(id)
		re := ref.eps[id]
		if ep == nil {
			t.Fatalf("seq %d step %d (%s): 端点 %q 在模拟中存在而实现中缺失", seq, step, o, id)
		}
		if ep.Inflight != re.inflight || ep.Draining != re.draining {
			t.Fatalf("seq %d step %d (%s): 端点 %q 账本不一致 实现(inflight=%d,draining=%v) 模拟(%d,%v)",
				seq, step, o, id, ep.Inflight, ep.Draining, re.inflight, re.draining)
		}
		est, last, has := ep.Est.Raw()
		if est != re.est || last != re.last || has != re.has {
			t.Fatalf("seq %d step %d (%s): 端点 %q 估计不一致 实现(%d,%d,%v) 模拟(%d,%d,%v)",
				seq, step, o, id, est, last, has, re.est, re.last, re.has)
		}
		if ep.Inflight < 0 || ep.Inflight > 1_000_000 {
			t.Fatalf("seq %d step %d (%s): 端点 %q 在途数越界 %d", seq, step, o, id, ep.Inflight)
		}
		sum += ep.Inflight
	}
	if sel.pl.Len() != len(ref.eps) {
		t.Fatalf("seq %d step %d (%s): 端点数不一致 实现=%d 模拟=%d", seq, step, o, sel.pl.Len(), len(ref.eps))
	}
	if sum != len(sel.tickets) {
		t.Fatalf("seq %d step %d (%s): Σ在途=%d != 未归还票据=%d", seq, step, o, sum, len(sel.tickets))
	}
	if sel.nextTicket != ref.nextTicket || sel.maxNow != ref.maxNow {
		t.Fatalf("seq %d step %d (%s): 票据号/maxNow 不一致 实现(%d,%d) 模拟(%d,%d)",
			seq, step, o, sel.nextTicket, sel.maxNow, ref.nextTicket, ref.maxNow)
	}
	for _, ep := range sel.pl.Eligible() {
		if ep.Draining {
			t.Fatalf("seq %d step %d (%s): 排空端点 %q 出现在候选集", seq, step, o, ep.ID)
		}
	}
}

func (s *sim) sortedIDs() []string {
	ids := make([]string, 0, len(s.eps))
	for id := range s.eps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func compareFinalState(t *testing.T, sel *Selector, ref *sim, seq int) {
	t.Helper()
	if len(sel.tickets) != len(ref.tickets) {
		t.Fatalf("seq %d: 未归还票据数不一致 实现=%d 模拟=%d", seq, len(sel.tickets), len(ref.tickets))
	}
	for tk, ep := range ref.tickets {
		if sel.tickets[tk] == nil || sel.tickets[tk].ID != ep.id {
			t.Fatalf("seq %d: 票据 %d 归属不一致", seq, tk)
		}
	}
}
