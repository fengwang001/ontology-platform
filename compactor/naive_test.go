package compactor

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// naiveRun 是朴素模拟里的运行；切片按新到旧排列（下标 0 最新）。
type naiveRun struct {
	id      int64
	size    int64
	created int64
	busy    bool
	fails   int
}

// naive 是完全独立、逐条照规格抄写的参考实现。
type naive struct {
	cfg      Config
	runs     []naiveRun
	nextRun  int64
	nextPlan int64
	now      int64
	plans    map[int64][]int64
	active   int
	probes   int64
}

func newNaive(cfg Config) *naive {
	return &naive{cfg: cfg, nextRun: 1, nextPlan: 1, plans: map[int64][]int64{}}
}

func naiveLe(a, b, c, d int64) bool {
	return new(big.Int).Mul(big.NewInt(a), big.NewInt(b)).Cmp(
		new(big.Int).Mul(big.NewInt(c), big.NewInt(d))) <= 0
}

func (m *naive) addRun(now, size int64) (int64, error) {
	if size < 1 || size > 1e12 || now < 0 {
		return 0, ErrParam
	}
	if now < m.now {
		return 0, ErrClock
	}
	m.now = now
	id := m.nextRun
	m.nextRun++
	m.runs = append([]naiveRun{{id: id, size: size, created: now}}, m.runs...)
	return id, nil
}

func (m *naive) span(i, j int) ([]int64, int64) {
	ids := make([]int64, 0, j-i)
	var total int64
	for k := i; k < j; k++ {
		ids = append(ids, m.runs[k].id)
		total += m.runs[k].size
	}
	return ids, total
}

// finalize 置忙、分配计划号，返回计划。seg 为运行下标区间。
func (m *naive) finalize(reason Reason, i, j int, now int64) Plan {
	for k := i; k < j; k++ {
		m.runs[k].busy = true
	}
	ids, total := m.span(i, j)
	pid := m.nextPlan
	m.nextPlan++
	m.plans[pid] = ids
	m.active++
	m.now = now
	return Plan{ID: pid, Reason: reason, Runs: ids, Total: total}
}

// pick 同时返回判定依据，便于日志复现。
func (m *naive) pick(now int64) (Plan, string, int64, error) {
	if now < 0 {
		return Plan{}, "now<0 -> ErrParam", m.probes, ErrParam
	}
	if now < m.now {
		return Plan{}, fmt.Sprintf("now %d < %d -> ErrClock", now, m.now), m.probes, ErrClock
	}
	if m.active >= m.cfg.Cmax {
		return Plan{}, fmt.Sprintf("active %d >= Cmax %d -> ErrBusy", m.active, m.cfg.Cmax), m.probes, ErrBusy
	}
	n := len(m.runs)

	if n >= m.cfg.MinRuns {
		anyBusy := false
		for _, r := range m.runs {
			if r.busy {
				anyBusy = true
			}
		}
		if !anyBusy {
			s := m.runs[n-1].size
			var e int64
			for i := 0; i < n-1; i++ {
				e += m.runs[i].size
			}
			why := fmt.Sprintf("SpaceAmp n=%d E=%d S=%d E*100=%d A*S=%d", n, e, s, e*100, m.cfg.A*s)
			if naiveLe(m.cfg.A, s, e, 100) {
				return m.finalize(ReasonSpaceAmp, 0, n, now), why + " -> FIRE all", m.probes, nil
			}
		}
	}

	if n >= m.cfg.MinRuns {
		for i := 0; i < n; i++ {
			m.probes++
			if m.runs[i].busy || m.runs[i].fails >= 2 {
				continue
			}
			acc := m.runs[i].size
			cnt := 1
			j := i + 1
			for j < n && cnt < m.cfg.MaxMerge {
				m.probes++
				if m.runs[j].busy || m.runs[j].fails >= 2 {
					break
				}
				if !naiveLe(m.runs[j].size, 100, acc, 100+m.cfg.Rho) {
					break
				}
				acc += m.runs[j].size
				cnt++
				j++
			}
			if cnt >= m.cfg.MinMerge {
				p := m.finalize(ReasonSizeRatio, i, j, now)
				return p, fmt.Sprintf("SizeRatio [%d..%d] acc=%d cnt=%d probes=%d -> FIRE", i, j-1, acc, cnt, m.probes), m.probes, nil
			}
		}
	}

	if n > m.cfg.MaxRuns {
		c := n - m.cfg.MaxRuns + 1
		if m.cfg.MaxMerge < c {
			c = m.cfg.MaxMerge
		}
		for i := 0; i+c <= n; i++ {
			ok := true
			for k := 0; k < c; k++ {
				if m.runs[i+k].busy {
					ok = false
				}
			}
			if ok {
				p := m.finalize(ReasonCountReduce, i, i+c, now)
				return p, fmt.Sprintf("CountReduce n=%d c=%d windowStart=%d -> FIRE", n, c, i), m.probes, nil
			}
		}
	}

	if m.cfg.P > 0 {
		for i := n - 1; i >= 0; i-- {
			r := m.runs[i]
			if !r.busy && now-r.created >= m.cfg.P {
				p := m.finalize(ReasonPeriodic, i, i+1, now)
				return p, fmt.Sprintf("Periodic idx=%d id=%d age=%d>=P=%d -> FIRE", i, r.id, now-r.created, m.cfg.P), m.probes, nil
			}
		}
	}

	return Plan{}, "no rule fired -> ErrNotNeeded", m.probes, ErrNotNeeded
}

func (m *naive) done(now, planID, out int64) error {
	if now < 0 || planID < 1 || out < 1 || out > 1e12 {
		return ErrParam
	}
	if now < m.now {
		return ErrClock
	}
	planIDs, ok := m.plans[planID]
	if !ok {
		return ErrUnknown
	}
	// 计划按运行身份跟踪：每次操作都重新定位下标，容忍后续在最新端插入。
	pos := map[int64]int{}
	for i, r := range m.runs {
		pos[r.id] = i
	}
	idxs := make([]int, len(planIDs))
	for k, id := range planIDs {
		idxs[k] = pos[id]
	}
	start, end := idxs[0], idxs[len(idxs)-1]+1
	id := m.nextRun
	m.nextRun++
	repl := naiveRun{id: id, size: out, created: now}
	updated := make([]naiveRun, 0, len(m.runs)-len(idxs)+1)
	updated = append(updated, m.runs[:start]...)
	updated = append(updated, repl)
	updated = append(updated, m.runs[end:]...)
	m.runs = updated
	delete(m.plans, planID)
	m.active--
	m.now = now
	return nil
}

func (m *naive) abort(planID int64) error {
	if planID < 1 {
		return ErrParam
	}
	planIDs, ok := m.plans[planID]
	if !ok {
		return ErrUnknown
	}
	for i := range m.runs {
		for _, id := range planIDs {
			if m.runs[i].id == id {
				m.runs[i].busy = false
				m.runs[i].fails++
			}
		}
	}
	delete(m.plans, planID)
	m.active--
	return nil
}

func (m *naive) snapshot() []Run {
	out := make([]Run, len(m.runs))
	for i, r := range m.runs {
		out[i] = Run{ID: r.id, Size: r.size, Created: r.created, Busy: r.busy, Fails: r.fails}
	}
	return out
}

func equalSnap(a, b []Run) bool {
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

func errKey(e error) string {
	switch {
	case e == nil:
		return ""
	case isErr(e, ErrParam):
		return "param"
	case isErr(e, ErrClock):
		return "clock"
	case isErr(e, ErrBusy):
		return "busy"
	case isErr(e, ErrUnknown):
		return "unknown"
	case isErr(e, ErrNotNeeded):
		return "notneeded"
	default:
		return e.Error()
	}
}

func isErr(err, target error) bool { return err == target }

func plansEqual(a, b Plan) bool {
	if a.ID != b.ID || a.Reason != b.Reason || a.Total != b.Total || len(a.Runs) != len(b.Runs) {
		return false
	}
	for i := range a.Runs {
		if a.Runs[i] != b.Runs[i] {
			return false
		}
	}
	return true
}

func randomConfig(rng *rand.Rand) Config {
	minR := 2 + rng.Intn(4)
	maxR := minR + rng.Intn(5)
	minM := 2 + rng.Intn(3)
	maxM := minM + rng.Intn(4)
	p := int64(0)
	if rng.Intn(2) == 0 {
		p = int64(1 + rng.Intn(20))
	}
	return Config{
		MinRuns:  minR,
		MaxRuns:  maxR,
		A:        int64(1 + rng.Intn(1_000)),
		Rho:      int64(rng.Intn(200)),
		MinMerge: minM,
		MaxMerge: maxM,
		Cmax:     1 + rng.Intn(3),
		P:        p,
	}
}

type opKind int

const (
	opAdd opKind = iota
	opPick
	opDone
	opAbort
)

type randOp struct {
	kind opKind
	now  int64
}

// TestNaiveDifferential 以 2000 组随机操作序列对照朴素实现，
// 日志打印每步输入、输出与判定依据。
func TestNaiveDifferential(t *testing.T) {
	const sequences = 2000
	const maxOps = 80
	rng := rand.New(rand.NewSource(20261002))

	for seq := 0; seq < sequences; seq++ {
		cfg := randomConfig(rng)
		s, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: New %v", seq, err)
		}
		m := newNaive(cfg)

		var clock int64
		var log strings.Builder
		fmt.Fprintf(&log, "=== seq %d cfg=%+v\n", seq, cfg)

		ops := maxOps/2 + rng.Intn(maxOps/2)
		for oi := 0; oi < ops; oi++ {
			var op randOp
			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
				10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
				20, 21, 22, 23, 24, 25, 26, 27, 28, 29,
				30, 31, 32, 33, 34, 35, 36, 37, 38, 39,
				40, 41, 42, 43, 44:
				op.kind = opAdd
			case 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89:
				op.kind = opDone
			case 90, 91, 92, 93, 94, 95, 96, 97, 98, 99:
				op.kind = opAbort
			default:
				op.kind = opPick
			}

			// 七成单调推进（含相等），一成注入倒退，其余沿用当前时钟。
			switch rng.Intn(10) {
			case 0:
				op.now = clock - int64(rng.Intn(3)+1)
			case 1:
				op.now = clock
			default:
				clock += int64(rng.Intn(5))
				op.now = clock
			}

			switch op.kind {
			case opAdd:
				size := int64(1 + rng.Intn(200))
				if rng.Intn(8) == 0 {
					size = int64(1 + rng.Intn(4))
				}
				if rng.Intn(40) == 0 {
					size = 0
				}
				id1, e1 := s.AddRun(op.now, size)
				id2, e2 := m.addRun(op.now, size)
				fmt.Fprintf(&log, "add(now=%d,size=%d) -> (%d,%v) | (%d,%v)\n", op.now, size, id1, e1, id2, e2)
				if id1 != id2 || errKey(e1) != errKey(e2) {
					failDiff(t, seq, log.String())
				}
				if e1 == nil && op.now > clock {
					clock = op.now
				}

			case opPick:
				s.mu.Lock()
				probesBefore := s.probes
				n := len(s.runs)
				s.mu.Unlock()

				p1, e1 := s.Pick(op.now)
				p2, why, probesM, e2 := m.pick(op.now)
				fmt.Fprintf(&log, "pick(now=%d) -> %+v(%v) | naive %+v(%v) [%s]\n", op.now, p1, e1, p2, e2, why)
				if errKey(e1) != errKey(e2) || (e1 == nil && !plansEqual(p1, p2)) {
					failDiff(t, seq, log.String())
				}
				s.mu.Lock()
				probesS := s.probes
				s.mu.Unlock()
				if probesS != probesM {
					t.Fatalf("seq %d probes %d != %d\n%s", seq, probesS, probesM, log.String())
				}
				if d := probesS - probesBefore; d > int64(n*cfg.MaxMerge) {
					t.Fatalf("seq %d probe budget exceeded: %d > %d*%d\n%s", seq, d, n, cfg.MaxMerge, log.String())
				}

			case opDone:
				// 从近期计划号里抽一个（含已结束的，制造 ErrUnknown）。
				pid := int64(1 + rng.Intn(int(m.nextPlan)+2))
				out := int64(1 + rng.Intn(300))
				if rng.Intn(40) == 0 {
					out = 0
				}
				e1 := s.Done(op.now, pid, out)
				e2 := m.done(op.now, pid, out)
				fmt.Fprintf(&log, "done(now=%d,plan=%d,out=%d) -> (%v) | (%v)\n", op.now, pid, out, e1, e2)
				if errKey(e1) != errKey(e2) {
					failDiff(t, seq, log.String())
				}

			case opAbort:
				pid := int64(1 + rng.Intn(int(m.nextPlan)+2))
				e1 := s.Abort(pid)
				e2 := m.abort(pid)
				fmt.Fprintf(&log, "abort(plan=%d) -> (%v) | (%v)\n", pid, e1, e2)
				if errKey(e1) != errKey(e2) {
					failDiff(t, seq, log.String())
				}
			}

			// 每步结束后运行集合必须完全一致。
			if !equalSnap(s.Runs(), m.snapshot()) {
				t.Fatalf("seq %d op %d state diverges:\n real=%v\nnaive=%v\n%s",
					seq, oi, s.Runs(), m.snapshot(), log.String())
			}
		}

		// 每个序列打印若干输入/输出/判定依据样本，证明日志可复现。
		if seq < 3 || seq%250 == 0 {
			t.Logf("\n%s", strings.TrimRight(log.String(), "\n"))
		}
	}
}

func failDiff(t *testing.T, seq int, log string) {
	t.Helper()
	t.Fatalf("seq %d mismatch:\n%s", seq, log)
}
