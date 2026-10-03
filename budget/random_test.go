package budget

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// sim 是按题述规则逐步写成的朴素模拟，全部用大整数运算，
// 用于与 Guard 的实现对照。
type sim struct {
	d     int
	p     []int
	fPct  int
	dmin  int
	b     *big.Int
	s     *big.Int
	cur   int
	fired []bool
	f     bool
}

func newSim(d int, p []int, fPct, dmin int, b int64) *sim {
	pc := make([]int, len(p))
	copy(pc, p)
	return &sim{
		d:     d,
		p:     pc,
		fPct:  fPct,
		dmin:  dmin,
		b:     big.NewInt(b),
		s:     new(big.Int),
		fired: make([]bool, len(pc)),
	}
}

// gePct 判断 s*100 >= b*pct，大整数比较。
func gePctBig(s, b *big.Int, pct int) bool {
	lhs := new(big.Int).Mul(s, big.NewInt(100))
	rhs := new(big.Int).Mul(b, big.NewInt(int64(pct)))
	return lhs.Cmp(rhs) >= 0
}

func (m *sim) frozen() bool { return m.s.Cmp(m.b) >= 0 }

func (m *sim) evaluate(z0 bool) []Event {
	var events []Event
	for i, pct := range m.p {
		if !m.fired[i] && gePctBig(m.s, m.b, pct) {
			m.fired[i] = true
			events = append(events, Event{Kind: EventLadder, Pct: pct})
		}
	}
	e := m.cur + 1
	cond := false
	if e >= m.dmin {
		proj := new(big.Int).Mul(m.s, big.NewInt(int64(m.d)))
		proj.Div(proj, big.NewInt(int64(e)))
		cond = gePctBig(proj, m.b, m.fPct)
	}
	if cond && !m.f {
		m.f = true
		events = append(events, Event{Kind: EventForecast})
	} else if !cond {
		m.f = false
	}
	z1 := m.frozen()
	if !z0 && z1 {
		events = append(events, Event{Kind: EventFroze})
	} else if z0 && !z1 {
		events = append(events, Event{Kind: EventThawed})
	}
	return events
}

var (
	bigZero     = new(big.Int)
	bigMaxSpend = big.NewInt(MaxSpend)
)

// spend 朴素模拟 Spend，返回事件或拒绝原因。
func (m *sim) spend(day int, x int64) ([]Event, *RejectReason) {
	if day < 0 || day >= m.d || x < -MaxSpendDelta || x > MaxSpendDelta {
		return nil, rejectPtr(RejectInvalidParam)
	}
	if day < m.cur {
		return nil, rejectPtr(RejectDayRegression)
	}
	if x > 0 && m.frozen() {
		return nil, rejectPtr(RejectFrozen)
	}
	ns := new(big.Int).Add(m.s, big.NewInt(x))
	if ns.Cmp(bigZero) < 0 || ns.Cmp(bigMaxSpend) > 0 {
		return nil, rejectPtr(RejectOutOfRange)
	}
	z0 := m.frozen()
	m.cur = day
	m.s = ns
	return m.evaluate(z0), nil
}

// adjust 朴素模拟 AdjustBudget。
func (m *sim) adjust(b2 int64) ([]Event, *RejectReason) {
	if b2 < 1 || b2 > MaxBudget {
		return nil, rejectPtr(RejectInvalidParam)
	}
	nb := big.NewInt(b2)
	z0 := m.frozen()
	for i, pct := range m.p {
		if m.fired[i] && !gePctBig(m.s, nb, pct) {
			m.fired[i] = false
		}
	}
	m.b = nb
	return m.evaluate(z0), nil
}

func rejectPtr(r RejectReason) *RejectReason { return &r }

func (m *sim) state() State {
	fired := make([]bool, len(m.fired))
	copy(fired, m.fired)
	return State{
		Spent:    m.s.Int64(),
		Cur:      m.cur,
		Budget:   m.b.Int64(),
		Fired:    fired,
		Forecast: m.f,
		Frozen:   m.frozen(),
	}
}

func eventsEqual(a, b []Event) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func randomBudget(rng *rand.Rand) int64 {
	switch rng.Intn(4) {
	case 0:
		return 1 + rng.Int63n(1000)
	case 1:
		return 1 + rng.Int63n(1_000_000)
	case 2:
		return 1 + rng.Int63n(1_000_000_000)
	default:
		return 1 + rng.Int63n(MaxBudget)
	}
}

// TestRandomAgainstNaiveSimulation 用 2000 组随机操作序列将 Guard 与按
// 规则逐步写成的朴素大整数模拟对照，日志打印输入、输出与判定依据。
func TestRandomAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		d := 1 + rng.Intn(366)
		k := 1 + rng.Intn(8)
		perm := rng.Perm(1000)
		p := make([]int, k)
		for i := range p {
			p[i] = perm[i] + 1
		}
		sortInts(p)
		fPct := 1 + rng.Intn(1000)
		dmin := 1 + rng.Intn(d)
		b := randomBudget(rng)

		g, err := NewGuard(d, p, fPct, dmin, b)
		if err != nil {
			t.Fatalf("seq=%d NewGuard(%d, %v, %d, %d, %d): %v", seq, d, p, fPct, dmin, b, err)
		}
		m := newSim(d, p, fPct, dmin, b)
		t.Logf("seq=%d config D=%d p=%v F=%d Dmin=%d B=%d", seq, d, p, fPct, dmin, b)

		ops := 20 + rng.Intn(40)
		for op := 0; op < ops; op++ {
			if rng.Intn(10) < 7 {
				day, x := randomSpendArgs(rng, m.cur, d, b)
				gEvents, gErr := g.Spend(day, x)
				mEvents, mReason := m.spend(day, x)
				checkOp(t, seq, op, fmt.Sprintf("Spend(%d, %d)", day, x),
					gEvents, gErr, mEvents, mReason, g, m)
			} else {
				b2 := randomBudget(rng)
				if rng.Intn(20) == 0 {
					b2 = MaxBudget + 1 + rng.Int63n(1000)
				}
				gEvents, gErr := g.AdjustBudget(b2)
				mEvents, mReason := m.adjust(b2)
				checkOp(t, seq, op, fmt.Sprintf("AdjustBudget(%d)", b2),
					gEvents, gErr, mEvents, mReason, g, m)
			}
		}
	}
}

func randomSpendArgs(rng *rand.Rand, cur, d int, b int64) (int, int64) {
	day := cur
	switch r := rng.Intn(10); {
	case r < 7:
		day = cur + rng.Intn(3)
		if day >= d {
			day = d - 1
		}
	case r < 9:
		day = rng.Intn(d)
	default:
		day = -1 + rng.Intn(d+2) // 可能越界
	}

	var x int64
	switch r := rng.Intn(20); {
	case r < 10:
		x = rng.Int63n(maxInt64(1, b/5) + 1)
	case r < 14:
		x = -rng.Int63n(maxInt64(1, b/5) + 1)
	case r < 18:
		x = rng.Int63n(MaxSpendDelta + 1)
		if rng.Intn(2) == 0 {
			x = -x
		}
	default:
		x = MaxSpendDelta + rng.Int63n(1000)
		if rng.Intn(2) == 0 {
			x = -x
		}
	}
	return day, x
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func checkOp(t *testing.T, seq, op int, input string,
	gEvents []Event, gErr error, mEvents []Event, mReason *RejectReason,
	g *Guard, m *sim) {
	t.Helper()

	var gReason *RejectReason
	if gErr != nil {
		rej, ok := gErr.(*RejectError)
		if !ok {
			t.Fatalf("seq=%d op=%d %s: unexpected error type %v", seq, op, input, gErr)
		}
		gReason = &rej.Reason
	}
	basis := fmt.Sprintf("basis S=%d B=%d cur=%d fired=%v f=%v frozen=%v",
		m.s, m.b, m.cur, m.fired, m.f, m.frozen())
	t.Logf("seq=%d op=%d input=%s events=%v reason=%v %s",
		seq, op, input, mEvents, reasonStr(mReason), basis)

	if (gReason == nil) != (mReason == nil) ||
		(gReason != nil && mReason != nil && *gReason != *mReason) {
		t.Fatalf("seq=%d op=%d %s: guard reason=%v, sim reason=%v (%s)",
			seq, op, input, reasonStr(gReason), reasonStr(mReason), basis)
	}
	if !eventsEqual(gEvents, mEvents) {
		t.Fatalf("seq=%d op=%d %s: guard events=%v, sim events=%v (%s)",
			seq, op, input, gEvents, mEvents, basis)
	}
	gs, ms := g.Snapshot(), m.state()
	if !reflect.DeepEqual(gs, ms) {
		t.Fatalf("seq=%d op=%d %s: guard state=%+v, sim state=%+v",
			seq, op, input, gs, ms)
	}
	if g.Frozen() != ms.Frozen {
		t.Fatalf("seq=%d op=%d %s: Frozen()=%v, want %v", seq, op, input, g.Frozen(), ms.Frozen)
	}
}

func reasonStr(r *RejectReason) string {
	if r == nil {
		return "<accepted>"
	}
	return r.String()
}
