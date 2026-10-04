package gate

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/band"
	"ontology/phase"
)

type errClass int

const (
	cNone errClass = iota
	cParam
	cClock
	cSymbol
	cPhase
	cHalted
	cLimit
	cStatic
	cDynamic
)

func classify(err error) errClass {
	switch {
	case err == nil:
		return cNone
	case errors.Is(err, ErrStatic):
		return cStatic
	case errors.Is(err, ErrDynamic):
		return cDynamic
	case errors.Is(err, ErrHalted):
		return cHalted
	case errors.Is(err, ErrLimit):
		return cLimit
	case errors.Is(err, ErrPhase):
		return cPhase
	case errors.Is(err, ErrSymbol):
		return cSymbol
	case errors.Is(err, ErrClock):
		return cClock
	case errors.Is(err, ErrParam):
		return cParam
	default:
		return -1
	}
}

func mustAdd(t *testing.T, g *Gate, sym string, p []int64) {
	t.Helper()
	if err := g.AddSymbol(sym, p[0], p[1], p[2], p[3], p[4], p[5], p[6],
		p[7], p[8], p[9], p[10], p[11]); err != nil {
		t.Fatalf("AddSymbol: %v", err)
	}
}

// 示例参数：prev=1000,L=1000,Ds=500,Dd=200,De=800,W=10,T=60,X=1,Hmax=3,open=0,close=10000,C=300
var exampleParams = []int64{1000, 1000, 500, 200, 800, 10, 60, 1, 3, 0, 10000, 300}

type step struct {
	op     string
	now    int64
	price  int64
	want   errClass
	filled bool
	halted bool
	reason Reason
	he     int64
	ext    bool
}

func runScenario(t *testing.T, params []int64, steps []step) {
	t.Helper()
	g := New()
	mustAdd(t, g, "S", params)
	for i, s := range steps {
		var (
			got             errClass
			fill, halt, ext bool
			reason          Reason
			he              int64
		)
		switch s.op {
		case "T":
			res, err := g.Trade(s.now, "S", s.price)
			got = classify(err)
			if res != nil {
				fill = res.Filled
				if res.Halt != nil {
					halt, reason, he = res.Halt.Halted, res.Halt.Reason, res.Halt.HE
				}
			}
		case "R":
			res, err := g.Resume(s.now, "S", s.price)
			got = classify(err)
			if res != nil {
				fill, ext, he = res.Filled, res.Extended, res.HE
			}
		case "C":
			got = classify(g.CheckOrder(s.now, "S", s.price))
		}
		if got != s.want || fill != s.filled || halt != s.halted ||
			(s.halted && (reason != s.reason || he != s.he)) ||
			(s.op == "R" && (ext != s.ext || (s.ext && he != s.he))) {
			t.Fatalf("step %d %s(now=%d,price=%d): got class=%d fill=%v halt=%v(%v,%d) ext=%v; want class=%d fill=%v halt=%v(%v,%d) ext=%v",
				i, s.op, s.now, s.price, got, fill, halt, reason, he, ext,
				s.want, s.filled, s.halted, s.reason, s.he, s.ext)
		}
	}
}

func heOf(r *TradeResult) int64 {
	if r != nil && r.Halt != nil {
		return r.Halt.HE
	}
	return -1
}

func TestSpecExample(t *testing.T) {
	// CheckOrder 与 Trade 共用单调时钟，分两个场景各自推进。
	runScenario(t, exampleParams, []step{
		{op: "C", now: 1, price: 900, want: cNone},
		{op: "C", now: 2, price: 1100, want: cNone},
		{op: "C", now: 3, price: 1101, want: cLimit},
		{op: "C", now: 4, price: 899, want: cLimit},
		{op: "C", now: 10000, price: 1100, want: cNone},
	})
	steps := []step{
		{op: "T", now: 1, price: 1010, want: cNone, filled: true},
		{op: "T", now: 5, price: 1020, want: cNone, filled: true},
		{op: "T", now: 11, price: 1031, want: cNone, halted: true, reason: ReasonDynamic, he: 71},
		{op: "T", now: 71, price: 1020, want: cHalted},
		{op: "T", now: 80, price: 1020, want: cHalted},
		{op: "T", now: 80, price: 1104, want: cHalted},
		{op: "R", now: 70, price: 1020, want: cPhase},
		{op: "R", now: 71, price: 1081, want: cNone, ext: true, he: 131},
		{op: "R", now: 131, price: 1081, want: cNone, filled: true},
		{op: "T", now: 135, price: 1100, want: cNone, filled: true},
		{op: "T", now: 141, price: 1104, want: cLimit},
		{op: "C", now: 141, price: 1100, want: cNone},
	}
	runScenario(t, exampleParams, steps)

	g := New()
	mustAdd(t, g, "S", exampleParams)
	g.Trade(11, "S", 1031)
	g.Resume(71, "S", 1081)
	g.Resume(131, "S", 1081)
	st := g.syms["S"]
	if st.book.Len() != 1 || st.book.At(0).Price != 1081 || st.book.Rs() != 1081 {
		t.Fatalf("恢复后簿状态异常: len=%d rs=%d", st.book.Len(), st.book.Rs())
	}

	g2 := New()
	mustAdd(t, g2, "S", exampleParams)
	p2 := append([]int64{1005}, exampleParams[1:]...)
	if err := g2.AddSymbol("S2", p2[0], p2[1], p2[2], p2[3], p2[4], p2[5], p2[6],
		p2[7], p2[8], p2[9], p2[10], p2[11]); err != nil {
		t.Fatal(err)
	}
	s2 := g2.syms["S2"]
	if s2.sym.Up() != 1105 || s2.sym.Dn() != 905 {
		t.Fatalf("prev=1005 up/dn=%d/%d want 1105/905", s2.sym.Up(), s2.sym.Dn())
	}
}

func TestStaticPriority(t *testing.T) {
	runScenario(t, exampleParams, []step{
		{op: "T", now: 1, price: 1060, want: cNone, halted: true, reason: ReasonStatic, he: 61},
	})
	runScenario(t, exampleParams, []step{
		{op: "T", now: 1, price: 1051, want: cNone, halted: true, reason: ReasonStatic, he: 61},
	})
}

func TestTailAndHmax(t *testing.T) {
	runScenario(t, exampleParams, []step{
		{op: "T", now: 9700, price: 1031, want: cDynamic},
		{op: "T", now: 9701, price: 1020, want: cNone, filled: true},
	})
	g := New()
	mustAdd(t, g, "S", exampleParams)
	res, err := g.Trade(9660, "S", 1031)
	if err != nil || !res.Halt.Halted || res.Halt.HE != 9700 {
		t.Fatalf("尾盘前中断 he=%d err=%v", heOf(res), err)
	}
	rr, err := g.Resume(9700, "S", 1081)
	if err != nil || !rr.Filled || rr.Extended {
		t.Fatalf("he==尾盘起点不应延长: %+v err=%v", rr, err)
	}
	p0 := append([]int64{}, exampleParams...)
	p0[8] = 0
	runScenario(t, p0, []step{
		{op: "T", now: 1, price: 1031, want: cDynamic},
		{op: "T", now: 2, price: 1020, want: cNone, filled: true},
	})
	p1 := append([]int64{}, exampleParams...)
	p1[8] = 1
	runScenario(t, p1, []step{
		{op: "T", now: 1, price: 1031, want: cNone, halted: true, reason: ReasonDynamic, he: 61},
		{op: "R", now: 61, price: 1000, want: cNone, filled: true},
		{op: "T", now: 62, price: 1031, want: cDynamic},
		{op: "T", now: 63, price: 1020, want: cNone, filled: true},
	})
}

func TestRejectionOrder(t *testing.T) {
	g := New()
	if err := g.AddSymbol("bad", 0, 1000, 500, 200, 800, 10, 60, 1, 3, 0, 10000, 300); !errors.Is(err, ErrParam) {
		t.Fatalf("prev=0 want ErrParam, got %v", err)
	}
	if err := g.AddSymbol("bad", 1000, 10001, 500, 200, 800, 10, 60, 1, 3, 0, 10000, 300); !errors.Is(err, ErrParam) {
		t.Fatalf("L 越界 want ErrParam, got %v", err)
	}
	if err := g.AddSymbol("bad", 1000, 1000, 500, 200, 800, 10, 60, 1, 3, 100, 100, 0); !errors.Is(err, ErrParam) {
		t.Fatalf("open>=close want ErrParam, got %v", err)
	}
	if err := g.AddSymbol("bad", 1000, 1000, 500, 200, 800, 10, 60, 1, 3, 0, 100, 101); !errors.Is(err, ErrParam) {
		t.Fatalf("C>close-open want ErrParam, got %v", err)
	}
	mustAdd(t, g, "S", exampleParams)
	p := exampleParams
	if err := g.AddSymbol("S", p[0], p[1], p[2], p[3], p[4], p[5], p[6],
		p[7], p[8], p[9], p[10], p[11]); !errors.Is(err, ErrSymbol) {
		t.Fatalf("重复 want ErrSymbol, got %v", err)
	}
	// 时钟回退先于标的不存在。
	g.CheckOrder(100, "S", 1000)
	if _, err := g.Trade(5, "nope", 1000); !errors.Is(err, ErrClock) {
		t.Fatalf("时钟回退先于标的, got %v", err)
	}
	if _, err := g.Trade(101, "nope", 1000); !errors.Is(err, ErrSymbol) {
		t.Fatalf("want ErrSymbol, got %v", err)
	}
	// 阶段（时段外）先于涨跌停。
	if _, err := g.Trade(10000, "S", 1104); !errors.Is(err, ErrPhase) {
		t.Fatalf("时段外 want ErrPhase, got %v", err)
	}
	// 时段内、连续状态：涨跌停先于超带（1104 也同时超静态带）。
	if _, err := g.Trade(9999, "S", 1104); !errors.Is(err, ErrLimit) {
		t.Fatalf("涨跌停先于超带, got %v", err)
	}
	if _, err := g.Resume(10002, "S", 1000); !errors.Is(err, ErrPhase) {
		t.Fatalf("未中断 Resume want ErrPhase, got %v", err)
	}
	// 被拒操作不推进时钟：回退到 50 仍应报时钟回退。
	if err := g.CheckOrder(50, "S", 1000); !errors.Is(err, ErrClock) {
		t.Fatalf("拒绝不应推进时钟, got %v", err)
	}
	// 参数非法先于时钟回退。
	if _, err := g.Trade(50, "", 1000); !errors.Is(err, ErrParam) {
		t.Fatalf("参数先于时钟, got %v", err)
	}
	// 被 CheckOrder 接受的涨跌停结果仍推进时钟（101 已接受过）。
	if err := g.CheckOrder(10001, "S", 1104); !errors.Is(err, ErrLimit) {
		t.Fatalf("CheckOrder 越界 want ErrLimit, got %v", err)
	}
}

// 取等与多 1：静态带、动态带分别覆盖，以及无更早成交回落 Rs 的动态判定。
func TestBandEqualityViaGate(t *testing.T) {
	// 宽松动态带（Dd=10000）下单独验证静态带 Ds=500：1050 取等成交、1051 中断。
	staticOnly := append([]int64{}, exampleParams...)
	staticOnly[3] = 10000
	runScenario(t, staticOnly, []step{
		{op: "T", now: 1, price: 1050, want: cNone, filled: true},
	})
	runScenario(t, staticOnly, []step{
		{op: "T", now: 1, price: 1051, want: cNone, halted: true, reason: ReasonStatic, he: 61},
	})
	// 动态带：Trade(5,1020) 取等已在示例；再验证下界取等 980 与多 1。
	runScenario(t, exampleParams, []step{
		{op: "T", now: 1, price: 1000, want: cNone, filled: true},
		{op: "T", now: 11, price: 980, want: cNone, filled: true},
	})
	runScenario(t, exampleParams, []step{
		{op: "T", now: 1, price: 1000, want: cNone, filled: true},
		{op: "T", now: 11, price: 979, want: cNone, halted: true, reason: ReasonDynamic, he: 71},
	})
	// now-W 恰好等于前一笔时刻：计入窗口之前，取该笔。
	runScenario(t, exampleParams, []step{
		{op: "T", now: 0, price: 1000, want: cNone, filled: true},
		{op: "T", now: 10, price: 1020, want: cNone, filled: true},
	})
}

// ---- 朴素线性扫描模型（每次遍历全部成交记录，绝不复用队列结构） ----

type naTick struct {
	time, price int64
}

type naiveModel struct {
	params      []int64
	maxNow      int64
	rs          int64
	ticks       []naTick
	state       phase.State
	he          int64
	halts       int64
	exts        int64
	lastRd      int64
	lastRdValid bool
}

func newNaive(p []int64) *naiveModel {
	return &naiveModel{params: p, rs: p[0], lastRd: p[0], state: phase.Cont}
}

func (m *naiveModel) inLimit(x int64) bool {
	s := band.New(band.Params{Prev: m.params[0], L: m.params[1]})
	return s.InLimit(x)
}

// rd 每次线性扫描全部历史成交，取时刻 <=now-W 的最后一笔；没有则按语义回落。
func (m *naiveModel) rd(now int64) int64 {
	cutoff := now - m.params[5]
	r := m.rs
	found := false
	for _, tk := range m.ticks {
		if tk.time <= cutoff {
			r, found = tk.price, true
		}
	}
	if !found {
		if m.lastRdValid {
			return m.lastRd
		}
		return m.rs
	}
	return r
}

// prune 在成交被接受后把老化记录固化到 lastRd 并删除（对应队列弹出）。
func (m *naiveModel) prune(now int64) (popped int) {
	cutoff := now - m.params[5]
	keep := m.ticks[:0]
	for _, tk := range m.ticks {
		if tk.time <= cutoff {
			m.lastRd = tk.price
			m.lastRdValid = true
			popped++
		} else {
			keep = append(keep, tk)
		}
	}
	m.ticks = append([]naTick{}, keep...)
	return popped
}

type modelOut struct {
	class  errClass
	fill   bool
	halt   bool
	reason Reason
	he     int64
	ext    bool
}

func (m *naiveModel) check(now int64, price int64) modelOut {
	m.maxNow = now
	if !m.inLimit(price) {
		return modelOut{class: cLimit}
	}
	return modelOut{class: cNone}
}

func (m *naiveModel) trade(now int64, price int64) modelOut {
	p := m.params
	if now < p[9] || now >= p[10] {
		return modelOut{class: cPhase}
	}
	if m.state == phase.Halt {
		return modelOut{class: cHalted}
	}
	if !m.inLimit(price) {
		return modelOut{class: cLimit}
	}
	tailStart := p[10] - p[11]
	haltIf := func(reason Reason) modelOut {
		if now >= tailStart || m.halts >= p[8] {
			cl := cStatic
			if reason == ReasonDynamic {
				cl = cDynamic
			}
			return modelOut{class: errClass(cl)}
		}
		he := now + p[6]
		if he > tailStart {
			he = tailStart
		}
		m.state = phase.Halt
		m.he = he
		m.exts = 0
		m.halts++
		m.maxNow = now
		return modelOut{halt: true, reason: reason, he: he}
	}
	if band.Over(price, m.rs, p[2]) {
		return haltIf(ReasonStatic)
	}
	if band.Over(price, m.rd(now), p[3]) {
		return haltIf(ReasonDynamic)
	}
	m.prune(now)
	m.ticks = append(m.ticks, naTick{now, price})
	m.maxNow = now
	return modelOut{class: cNone, fill: true}
}

func (m *naiveModel) resume(now int64, price int64) modelOut {
	p := m.params
	if now < p[9] || now >= p[10] {
		return modelOut{class: cPhase}
	}
	if m.state != phase.Halt {
		return modelOut{class: cPhase}
	}
	if now < m.he {
		return modelOut{class: cPhase}
	}
	if !m.inLimit(price) {
		return modelOut{class: cLimit}
	}
	tailStart := p[10] - p[11]
	if band.Over(price, m.rs, p[4]) && m.exts < p[7] && m.he < tailStart {
		he := m.he + p[6]
		if he > tailStart {
			he = tailStart
		}
		m.he = he
		m.exts++
		m.maxNow = now
		return modelOut{ext: true, he: he}
	}
	m.state = phase.Cont
	m.he = 0
	m.exts = 0
	m.rs = price
	m.lastRd = price
	m.lastRdValid = true
	m.ticks = []naTick{{now, price}}
	m.maxNow = now
	return modelOut{fill: true}
}

// ---- 1500 组随机序列：朴素线性扫描 vs 闸门 ----

type opRec struct {
	kind  string
	now   int64
	price int64
	gate  modelOut
	naive modelOut
	why   string
}

func randParams(rng *rand.Rand) []int64 {
	prev := int64(rng.Intn(2000)) + 1
	l := int64(rng.Intn(3000)) + 1
	ds := int64(rng.Intn(1000)) + 1
	dd := int64(rng.Intn(400)) + 1
	de := int64(rng.Intn(1200)) + 1
	w := int64(rng.Intn(20)) + 1
	t := int64(rng.Intn(40)) + 1
	x := int64(rng.Intn(3))
	hmax := int64(rng.Intn(4))
	open := int64(0)
	closeT := int64(200 + rng.Intn(300))
	c := int64(rng.Intn(int(closeT) + 1))
	return []int64{prev, l, ds, dd, de, w, t, x, hmax, open, closeT, c}
}

func gateOut(op string, res interface{}, err error) modelOut {
	o := modelOut{class: classify(err)}
	if tr, ok := res.(*TradeResult); ok && tr != nil {
		o.fill = tr.Filled
		if tr.Halt != nil {
			o.halt, o.reason, o.he = tr.Halt.Halted, tr.Halt.Reason, tr.Halt.HE
		}
	}
	if rr, ok := res.(*ResumeResult); ok && rr != nil {
		o.fill, o.ext, o.he = rr.Filled, rr.Extended, rr.HE
	}
	return o
}

func verifyInvariants(t *testing.T, g *Gate, ops []opRec, idx int) {
	t.Helper()
	st := g.syms["S"]
	p := st.sym.P()
	if st.ph.HaltCount() > p.Hmax {
		t.Fatalf("op %d: 中断次数 %d 超过 Hmax %d", idx, st.ph.HaltCount(), p.Hmax)
	}
	if st.ph.State() == phase.Halt && st.ph.HE() > p.Close-p.C {
		t.Fatalf("op %d: he %d 超过尾盘起点 %d", idx, st.ph.HE(), p.Close-p.C)
	}
	for k := 0; k < st.book.Len(); k++ {
		tk := st.book.At(k)
		if !st.sym.InLimit(tk.Price) {
			t.Fatalf("op %d: 成交价 %d 越界", idx, tk.Price)
		}
	}
}

func runRandomSequence(t *testing.T, seed int64, verbose bool) []opRec {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	params := randParams(rng)
	g := New()
	mustAdd(t, g, "S", params)
	m := newNaive(params)
	n := 30 + rng.Intn(40)
	now := int64(0)
	ops := make([]opRec, 0, n)
	fills := 0
	for i := 0; i < n; i++ {
		now += int64(rng.Intn(12))
		kind := []string{"T", "T", "T", "R", "C"}[rng.Intn(5)]
		price := int64(rng.Intn(int(params[0]))) + 1
		if rng.Intn(3) == 0 {
			price = params[0] + int64(rng.Intn(int(params[0]/2+1))) - params[0]/4
		}
		var (
			go_ modelOut
			mo  modelOut
			why string
		)
		switch kind {
		case "T":
			res, err := g.Trade(now, "S", price)
			go_ = gateOut(kind, res, err)
			mo = m.trade(now, price)
			why = tradeWhy(m, now, price)
		case "R":
			res, err := g.Resume(now, "S", price)
			go_ = gateOut(kind, res, err)
			mo = m.resume(now, price)
			why = fmt.Sprintf("state=%d he=%d exts=%d rs=%d de-over=%v",
				m.state, m.he, m.exts, m.rs, band.Over(price, m.rs, params[4]))
		case "C":
			err := g.CheckOrder(now, "S", price)
			go_ = gateOut(kind, nil, err)
			mo = m.check(now, price)
			why = fmt.Sprintf("limit in [%d,%d]", band.New(band.Params{Prev: params[0], L: params[1]}).Dn(),
				band.New(band.Params{Prev: params[0], L: params[1]}).Up())
		}
		rec := opRec{kind, now, price, go_, mo, why}
		ops = append(ops, rec)
		if go_.fill {
			fills++
		}
		if go_ != mo {
			for j, o := range ops {
				t.Logf("seed=%d params=%v", seed, params)
				t.Logf("  op[%d] %s now=%d price=%d gate={c%d f%v h%v(%v,%d) e%v} naive={c%d f%v h%v(%v,%d) e%v} | %s",
					j, o.kind, o.now, o.price,
					o.gate.class, o.gate.fill, o.gate.halt, o.gate.reason, o.gate.he, o.gate.ext,
					o.naive.class, o.naive.fill, o.naive.halt, o.naive.reason, o.naive.he, o.naive.ext, o.why)
			}
			t.Fatalf("seed=%d op %d: gate %+v != naive %+v", seed, i, go_, mo)
		}
		if verbose {
			t.Logf("seed=%d op[%d] %s now=%d price=%d -> class=%d fill=%v halt=%v(%v,%d) ext=%v | %s",
				seed, i, kind, now, price, go_.class, go_.fill, go_.halt, go_.reason, go_.he, go_.ext, why)
		}
		verifyInvariants(t, g, ops, i)
		if st := g.syms["S"]; st.book.Popped() > fills {
			t.Fatalf("seed=%d op %d: popped=%d 超过成交总数 %d", seed, i, st.book.Popped(), fills)
		}
	}
	return ops
}

func tradeWhy(m *naiveModel, now, price int64) string {
	p := m.params
	rd := m.rd(now)
	return fmt.Sprintf("state=%d rs=%d rd=%d ds-over=%v dd-over=%v limit=%v tail=%d halts=%d/%d",
		m.state, m.rs, rd, band.Over(price, m.rs, p[2]), band.Over(price, rd, p[3]),
		m.inLimit(price), p[10]-p[11], m.halts, p[8])
}

func TestRandomVsNaive(t *testing.T) {
	for seed := int64(1); seed <= 1500; seed++ {
		verbose := seed <= 3
		if verbose {
			t.Logf("==== 随机序列 seed=%d 全量输入/输出/判定依据 ====", seed)
		}
		runRandomSequence(t, seed, verbose)
		if seed%300 == 0 {
			t.Logf("已完成 %d/1500 组随机对照", seed)
		}
	}
}

// 窗口内 10 笔与 10000 笔两档：弹出总数 ≤ 成交总数，单次 Trade 平均触碰摊还为常数。
func TestPoppedAmortized(t *testing.T) {
	for _, n := range []int{10, 10000} {
		// W 很大使前 n 笔始终留在窗口内，最后一笔把它们一次性老化。
		params := []int64{1_000_000_000, 10000, 10000, 10000, 10000,
			1_000_000, 60, 1, 0, 0, 1_000_000_000, 0}
		g := New()
		mustAdd(t, g, "S", params)
		for i := 0; i < n; i++ {
			if _, err := g.Trade(int64(i), "S", 1_000_000_000); err != nil {
				t.Fatalf("n=%d fill %d: %v", n, i, err)
			}
		}
		st := g.syms["S"]
		if before := st.book.Popped(); before != 0 {
			t.Fatalf("n=%d: 窗口内不应有弹出, got %d", n, before)
		}
		// now-W 越过全部旧记录：一次成交弹出 n 条；每条记录一生只弹一次。
		lastNow := int64(n-1) + 1_000_000 + 1
		if _, err := g.Trade(lastNow, "S", 1_000_000_000); err != nil {
			t.Fatalf("n=%d final trade: %v", n, err)
		}
		popped := st.book.Popped()
		fills := n + 1
		if popped != n {
			t.Fatalf("n=%d: popped=%d want %d", n, popped, n)
		}
		if popped > fills {
			t.Fatalf("n=%d: popped %d > 成交总数 %d", n, popped, fills)
		}
		t.Logf("窗口档位 %d 笔：弹出 %d / 成交 %d，单次 Trade 平均触碰 %.4f（与窗口笔数无关，恒 ≤1）",
			n, popped, fills, float64(popped)/float64(fills))
	}
}

// 相同操作序列重放结果一致。
func TestDeterministicReplay(t *testing.T) {
	ops := runRandomSequence(t, 42, false)
	params := func() []int64 {
		rng := rand.New(rand.NewSource(42))
		return randParams(rng)
	}()
	g := New()
	mustAdd(t, g, "S", params)
	for i, o := range ops {
		var out modelOut
		switch o.kind {
		case "T":
			res, err := g.Trade(o.now, "S", o.price)
			out = gateOut(o.kind, res, err)
		case "R":
			res, err := g.Resume(o.now, "S", o.price)
			out = gateOut(o.kind, res, err)
		case "C":
			out = gateOut(o.kind, nil, g.CheckOrder(o.now, "S", o.price))
		}
		if out != o.gate {
			t.Fatalf("重放 op %d 不一致: %+v vs %+v", i, out, o.gate)
		}
	}
}

// 并发：每个 goroutine 独占标的、各自时钟单调；-race 下结果与串行一致。
func TestConcurrentSymbols(t *testing.T) {
	const workers = 16
	g := New()
	for w := 0; w < workers; w++ {
		mustAdd(t, g, fmt.Sprintf("sym%d", w), exampleParams)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			sym := fmt.Sprintf("sym%d", w)
			rng := rand.New(rand.NewSource(int64(w + 1)))
			now := int64(0)
			for i := 0; i < 200; i++ {
				now += 1 + int64(rng.Intn(5))
				price := int64(1000 + rng.Intn(120) - 60)
				if rng.Intn(4) == 0 {
					g.Resume(now, sym, price)
				} else {
					g.Trade(now, sym, price)
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	for w := 0; w < workers; w++ {
		st := g.syms[fmt.Sprintf("sym%d", w)]
		if st.ph.HaltCount() > 3 || (st.ph.State() == phase.Halt &&
			st.ph.HE() > 9700) {
			t.Fatalf("worker %d 不变量被破坏: halts=%d he=%d", w, st.ph.HaltCount(), st.ph.HE())
		}
	}
}
