package lot_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/lot"
	"ontology/plan"
)

// 朴素模拟：保存进入当前严格度以来的全部初检历史，转移时逐条回看，
// 与被测实现的有界窗口对照。错误检查次序与 lot.Manager 完全一致。

type simRec struct {
	rejected bool
	d        int
}

type simStream struct {
	sev     plan.Severity
	hist    []simRec // 进入当前严格度以来的全部初检记录
	pending string
}

type simLot struct {
	key     lot.StreamKey
	status  lot.Status
	n       int
	pl      plan.Plan
	sev     plan.Severity
	d       int
	decided bool
	retest  bool
}

type naive struct {
	table   *plan.Table
	streams map[lot.StreamKey]*simStream
	lots    map[string]*simLot
}

func newNaive(tb *plan.Table) *naive {
	return &naive{
		table:   tb,
		streams: make(map[lot.StreamKey]*simStream),
		lots:    make(map[string]*simLot),
	}
}

func (s *naive) submit(key lot.StreamKey, id string, n int) (lot.Lot, error) {
	if key.Supplier == "" || key.Material == "" || id == "" || n < 1 || n > 1_000_000 {
		return lot.Lot{}, lot.ErrInvalidParam
	}
	if _, ok := s.table.Lookup(n); !ok {
		return lot.Lot{}, lot.ErrInvalidParam
	}
	st := s.streams[key]
	if st != nil && st.sev == plan.Suspended {
		return lot.Lot{}, lot.ErrSuspended
	}
	if st != nil && st.pending != "" {
		return lot.Lot{}, lot.ErrState
	}
	if _, dup := s.lots[id]; dup {
		return lot.Lot{}, lot.ErrConflict
	}
	if st == nil {
		st = &simStream{sev: plan.Normal}
		s.streams[key] = st
	}
	p, _ := s.table.Plan(n, st.sev)
	if p.N > n {
		p.N = n
	}
	s.lots[id] = &simLot{key: key, status: lot.Pending, n: n, pl: p, sev: st.sev}
	st.pending = id
	return lot.Lot{ID: id, Stream: key, N: n, Severity: st.sev, Plan: p, Status: lot.Pending}, nil
}

func (s *naive) record(id string, d int, op lot.Operator) (lot.Lot, string, error) {
	if id == "" || d < 0 {
		return lot.Lot{}, "", lot.ErrInvalidParam
	}
	if !op.Has(lot.RoleInspector) {
		return lot.Lot{}, "", lot.ErrPermission
	}
	sl, ok := s.lots[id]
	if !ok {
		return lot.Lot{}, "", lot.ErrNotFound
	}
	if sl.status != lot.Pending {
		return lot.Lot{}, "", lot.ErrState
	}
	if d > sl.pl.N {
		return lot.Lot{}, "", lot.ErrOutOfRange
	}
	st := s.streams[sl.key]
	st.pending = ""
	sl.d = d
	sl.decided = true
	rejected := d >= sl.pl.Re
	marginal := !rejected && d > sl.pl.Ac
	var basis string
	switch {
	case marginal:
		basis = fmt.Sprintf("Ac=%d<d=%d<Re=%d 边缘接收", sl.pl.Ac, d, sl.pl.Re)
	case rejected:
		basis = fmt.Sprintf("d=%d>=Re=%d 拒收", d, sl.pl.Re)
	default:
		basis = fmt.Sprintf("d=%d<=Ac=%d 接收", d, sl.pl.Ac)
	}
	if sl.retest {
		if rejected {
			sl.status = lot.Scrapped
		} else {
			sl.status = lot.Released
		}
		return snapshot(id, sl), basis + "（复检，不计数不转移）", nil
	}
	if rejected {
		sl.status = lot.Rejected
	} else {
		sl.status = lot.Released
	}
	st.hist = append(st.hist, simRec{rejected: rejected, d: d})
	basis += "；" + st.transit(s.table.Lr(), marginal)
	return snapshot(id, sl), basis, nil
}

// transit 逐条回看全部历史并执行转移，返回判定依据。
func (st *simStream) transit(lr int, marginal bool) string {
	switch st.sev {
	case plan.Normal:
		n := len(st.hist)
		from := n - 5
		if from < 0 {
			from = 0
		}
		rej := 0
		for _, r := range st.hist[from:] {
			if r.rejected {
				rej++
			}
		}
		if rej >= 2 {
			st.sev, st.hist = plan.Tightened, nil
			return fmt.Sprintf("Normal 最近5批拒收=%d>=2 -> Tightened", rej)
		}
		if n >= 10 {
			sum, all := 0, true
			for _, r := range st.hist[n-10:] {
				if r.rejected {
					all = false
				} else {
					sum += r.d
				}
			}
			if all && sum <= lr {
				st.sev, st.hist = plan.Reduced, nil
				return fmt.Sprintf("Normal 最近10批全收且d之和=%d<=Lr=%d -> Reduced", sum, lr)
			}
		}
		return fmt.Sprintf("Normal 保持（最近5批拒收=%d）", rej)
	case plan.Tightened:
		last := st.hist[len(st.hist)-1]
		if last.rejected {
			rejects := 0
			for _, r := range st.hist {
				if r.rejected {
					rejects++
				}
			}
			if rejects >= 5 {
				st.sev, st.hist = plan.Suspended, nil
				return fmt.Sprintf("Tightened 累计拒收=%d>=5 -> Suspended", rejects)
			}
			return fmt.Sprintf("Tightened 保持（累计拒收=%d）", rejects)
		}
		consec := 0
		for i := len(st.hist) - 1; i >= 0 && !st.hist[i].rejected; i-- {
			consec++
		}
		if consec >= 5 {
			st.sev, st.hist = plan.Normal, nil
			return fmt.Sprintf("Tightened 连续接收=%d>=5 -> Normal", consec)
		}
		return fmt.Sprintf("Tightened 保持（连续接收=%d）", consec)
	case plan.Reduced:
		if st.hist[len(st.hist)-1].rejected || marginal {
			st.sev, st.hist = plan.Normal, nil
			return "Reduced 拒收或边缘接收 -> Normal"
		}
		return "Reduced 保持"
	}
	return ""
}

func (s *naive) resubmit(id string) (lot.Lot, error) {
	if id == "" {
		return lot.Lot{}, lot.ErrInvalidParam
	}
	sl, ok := s.lots[id]
	if !ok {
		return lot.Lot{}, lot.ErrNotFound
	}
	if sl.status != lot.Rejected {
		return lot.Lot{}, lot.ErrState
	}
	st := s.streams[sl.key]
	if st.pending != "" {
		return lot.Lot{}, lot.ErrState
	}
	p, _ := s.table.Plan(sl.n, plan.Tightened)
	if p.N > sl.n {
		p.N = sl.n
	}
	sl.pl = p
	sl.sev = plan.Tightened
	sl.status = lot.Pending
	sl.d = 0
	sl.decided = false
	sl.retest = true
	st.pending = id
	return snapshot(id, sl), nil
}

func (s *naive) resume(key lot.StreamKey, op lot.Operator) error {
	if key.Supplier == "" || key.Material == "" {
		return lot.ErrInvalidParam
	}
	if !op.Has(lot.RoleManager) {
		return lot.ErrPermission
	}
	st, ok := s.streams[key]
	if !ok {
		return lot.ErrNotFound
	}
	if st.sev != plan.Suspended {
		return lot.ErrState
	}
	st.sev = plan.Tightened
	st.hist = nil
	return nil
}

func (s *naive) severity(key lot.StreamKey) (plan.Severity, bool) {
	st, ok := s.streams[key]
	if !ok {
		return plan.Normal, false
	}
	return st.sev, true
}

func snapshot(id string, sl *simLot) lot.Lot {
	return lot.Lot{
		ID: id, Stream: sl.key, N: sl.n, Severity: sl.sev, Plan: sl.pl,
		Status: sl.status, D: sl.d, Decided: sl.decided, Retest: sl.retest,
	}
}

// category 把错误映射为拒绝类别，用于对照。
func category(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, lot.ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, lot.ErrPermission):
		return "permission"
	case errors.Is(err, lot.ErrNotFound):
		return "not-found"
	case errors.Is(err, lot.ErrSuspended):
		return "suspended"
	case errors.Is(err, lot.ErrState):
		return "state"
	case errors.Is(err, lot.ErrConflict):
		return "conflict"
	case errors.Is(err, lot.ErrOutOfRange):
		return "out-of-range"
	}
	return "unknown"
}

type randOp struct {
	kind string
	key  lot.StreamKey
	id   string
	n, d int
	op   lot.Operator
}

func genScript(rng *rand.Rand, keys []lot.StreamKey, ops []lot.Operator) []randOp {
	validN := []int{1, 3, 25, 60, 100, 150, 200, 1000, 1_000_000}
	badN := []int{0, -1, 1_000_001}
	script := make([]randOp, 60)
	for i := range script {
		var o randOp
		switch r := rng.Intn(100); {
		case r < 40:
			o.kind = "submit"
		case r < 70:
			o.kind = "record"
		case r < 85:
			o.kind = "resubmit"
		default:
			o.kind = "resume"
		}
		o.key = keys[rng.Intn(len(keys))]
		if rng.Intn(20) == 0 {
			o.key = lot.StreamKey{Supplier: "", Material: "m"}
		}
		o.id = fmt.Sprintf("L%d", rng.Intn(30))
		if rng.Intn(30) == 0 {
			o.id = ""
		}
		if rng.Intn(5) == 0 {
			o.n = badN[rng.Intn(len(badN))]
		} else {
			o.n = validN[rng.Intn(len(validN))]
		}
		if rng.Intn(5) == 0 {
			o.d = rng.Intn(60) - 2 // 可能为负或超过 n
		} else {
			o.d = rng.Intn(6)
		}
		o.op = ops[rng.Intn(len(ops))]
		script[i] = o
	}
	return script
}

// TestRandomAgainstNaive 用 1500 组随机操作序列对照被测实现与朴素模拟：
// 每个操作的拒绝类别、成功后的批状态、每个流的严格度轨迹都必须一致；
// 同一序列在两个独立 Manager 上重放，验证结果可精确复现。
// 日志打印每步的输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	keys := []lot.StreamKey{
		{Supplier: "s1", Material: "m1"},
		{Supplier: "s1", Material: "m2"},
		{Supplier: "s2", Material: "m1"},
	}
	both := lot.Operator{ID: "both", Roles: []lot.Role{lot.RoleInspector, lot.RoleManager}}
	ops := []lot.Operator{insp, mgr, both, none}
	for seed := int64(1); seed <= 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		script := genScript(rng, keys, ops)
		tb := stdTable(t)
		m1 := lot.NewManager(tb)
		m2 := lot.NewManager(tb) // 重放
		sim := newNaive(tb)
		for i, o := range script {
			var l1, l2, ls lot.Lot
			var e1, e2, es error
			basis := ""
			switch o.kind {
			case "submit":
				l1, e1 = m1.Submit(o.key, o.id, o.n)
				l2, e2 = m2.Submit(o.key, o.id, o.n)
				ls, es = sim.submit(o.key, o.id, o.n)
			case "record":
				l1, e1 = m1.Record(o.id, o.d, o.op)
				l2, e2 = m2.Record(o.id, o.d, o.op)
				ls, basis, es = sim.record(o.id, o.d, o.op)
			case "resubmit":
				l1, e1 = m1.Resubmit(o.id)
				l2, e2 = m2.Resubmit(o.id)
				ls, es = sim.resubmit(o.id)
			case "resume":
				e1 = m1.Resume(o.key, o.op)
				e2 = m2.Resume(o.key, o.op)
				es = sim.resume(o.key, o.op)
			}
			c1, c2, cs := category(e1), category(e2), category(es)
			t.Logf("seed=%d op=%02d %s key=%+v id=%q n=%d d=%d -> cat=%s lot=%+v %s",
				seed, i, o.kind, o.key, o.id, o.n, o.d, c1, l1, basis)
			if c1 != cs || c2 != cs {
				t.Fatalf("seed=%d op=%d %s: 类别不一致 real=%s replay=%s naive=%s",
					seed, i, o.kind, c1, c2, cs)
			}
			if o.kind != "resume" && c1 == "ok" && (l1 != ls || l2 != ls) {
				t.Fatalf("seed=%d op=%d %s: 批状态不一致 real=%+v replay=%+v naive=%+v",
					seed, i, o.kind, l1, l2, ls)
			}
		}
		for _, k := range keys {
			s1, ok1 := m1.SeverityOf(k)
			s2, ok2 := m2.SeverityOf(k)
			ss, oks := sim.severity(k)
			if s1 != ss || s2 != ss || ok1 != oks || ok2 != oks {
				t.Fatalf("seed=%d: 流 %+v 严格度不一致 real=%v(%v) replay=%v(%v) naive=%v(%v)",
					seed, k, s1, ok1, s2, ok2, ss, oks)
			}
		}
	}
}
