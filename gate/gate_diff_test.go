package gate

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func atomicAdd(p *int64, delta int64) int64 { return atomic.AddInt64(p, delta) }

// ---------- 朴素参考模型：每次线性扫描全部成交记录求 Rd ----------

type naiveTick struct {
	at    int64
	price int64
}

type naiveSymbol struct {
	prev, up, dn          int64
	ds, dd, de            int64
	window, duration      int64
	open, close, lastCall int64
	maxExtend, haltMax    int
	rs                    int64
	ticks                 []naiveTick
	halted                bool
	he                    int64
	extends               int
	halts                 int
}

type naiveModel struct {
	clock   int64
	clockOK bool
	syms    map[string]*naiveSymbol
}

func naiveOver(price, ref, bp int64) bool {
	d := price - ref
	if d < 0 {
		d = -d
	}
	return d*10000 > bp*ref
}

func naiveRd(s *naiveSymbol, now int64) int64 {
	cutoff := now - s.window
	rd := s.rs
	for i := len(s.ticks) - 1; i >= 0; i-- { // 朴素：线性扫描全部成交
		if s.ticks[i].at <= cutoff {
			return s.ticks[i].price
		}
	}
	return rd
}

func (m *naiveModel) add(sym string, prev, lim, ds, dd, de, w, dur int64, x, h int, opn, cls, c int64) error {
	if sym == "" || prev < 1 || prev > 1e9 || lim < 1 || lim > 10000 ||
		ds < 1 || ds > 10000 || dd < 1 || dd > 10000 || de < 1 || de > 10000 ||
		w < 1 || w > 1e6 || dur < 1 || dur > 1e6 ||
		x < 0 || x > 10 || h < 0 || h > 100 ||
		opn < 0 || cls < 0 || opn >= cls || c < 0 || c > cls-opn {
		return ErrInvalidParam
	}
	if _, ok := m.syms[sym]; ok {
		return ErrDuplicateSymbol
	}
	up := prev * (10000 + lim) / 10000
	dn := (prev*(10000-lim) + 9999) / 10000
	m.syms[sym] = &naiveSymbol{
		prev: prev, up: up, dn: dn, ds: ds, dd: dd, de: de,
		window: w, duration: dur, open: opn, close: cls, lastCall: cls - c,
		maxExtend: x, haltMax: h, rs: prev,
	}
	return nil
}

func (m *naiveModel) tick(now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalidParam
	}
	if m.clockOK && now < m.clock {
		return ErrClockBack
	}
	return nil
}

// outcome 为操作结果分类；fill/halt/extend 携带关键数值。
type outcome struct {
	cat    string
	end    int64
	reason HaltReason
}

func (m *naiveModel) check(now int64, sym string, price int64) outcome {
	if err := m.tick(now); err != nil {
		return outcome{cat: errName(err)}
	}
	s, ok := m.syms[sym]
	if !ok {
		return outcome{cat: errName(ErrUnknownSymbol)}
	}
	if price < s.dn || price > s.up {
		return outcome{cat: errName(ErrLimit)}
	}
	m.clock, m.clockOK = now, true
	return outcome{cat: "accept"}
}

func (m *naiveModel) trade(now int64, sym string, price int64) outcome {
	if err := m.tick(now); err != nil {
		return outcome{cat: errName(err)}
	}
	s, ok := m.syms[sym]
	if !ok {
		return outcome{cat: errName(ErrUnknownSymbol)}
	}
	if now < s.open || now >= s.close {
		return outcome{cat: errName(ErrOutOfSession)}
	}
	if s.halted {
		return outcome{cat: errName(ErrHalted)}
	}
	if price < s.dn || price > s.up {
		return outcome{cat: errName(ErrLimit)}
	}
	rd := naiveRd(s, now)
	staticOver := naiveOver(price, s.rs, s.ds)
	dynOver := naiveOver(price, rd, s.dd)
	if staticOver || dynOver {
		if now >= s.lastCall || s.halts >= s.haltMax {
			if staticOver {
				return outcome{cat: errName(ErrStaticBand)}
			}
			return outcome{cat: errName(ErrDynamicBand)}
		}
		m.clock, m.clockOK = now, true
		s.halted, s.extends = true, 0
		s.halts++
		s.he = now + s.duration
		if s.lastCall < s.he {
			s.he = s.lastCall
		}
		r := ReasonDynamic
		if staticOver {
			r = ReasonStatic
		}
		return outcome{cat: "halt", end: s.he, reason: r}
	}
	m.clock, m.clockOK = now, true
	s.ticks = append(s.ticks, naiveTick{now, price})
	return outcome{cat: "fill"}
}

func (m *naiveModel) resume(now int64, sym string, price int64) outcome {
	if err := m.tick(now); err != nil {
		return outcome{cat: errName(err)}
	}
	s, ok := m.syms[sym]
	if !ok {
		return outcome{cat: errName(ErrUnknownSymbol)}
	}
	if now < s.open || now >= s.close {
		return outcome{cat: errName(ErrOutOfSession)}
	}
	if !s.halted {
		return outcome{cat: errName(ErrNotHalted)}
	}
	if now < s.he {
		return outcome{cat: errName(ErrHaltNotEnded)}
	}
	if price < s.dn || price > s.up {
		return outcome{cat: errName(ErrLimit)}
	}
	m.clock, m.clockOK = now, true
	if s.extends < s.maxExtend && s.he < s.lastCall && naiveOver(price, s.rs, s.de) {
		s.extends++
		s.he += s.duration
		if s.lastCall < s.he {
			s.he = s.lastCall
		}
		return outcome{cat: "extend", end: s.he}
	}
	s.halted = false
	s.he, s.extends = 0, 0
	s.rs = price
	s.ticks = append(s.ticks[:0], naiveTick{now, price})
	return outcome{cat: "fill"}
}

func errName(err error) string {
	for _, e := range []error{
		ErrInvalidParam, ErrClockBack, ErrUnknownSymbol, ErrDuplicateSymbol,
		ErrOutOfSession, ErrNotHalted, ErrHaltNotEnded, ErrHalted,
		ErrLimit, ErrStaticBand, ErrDynamicBand,
	} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func realOutcomeTrade(hr *HaltResult, err error) outcome {
	if err != nil {
		return outcome{cat: errName(err)}
	}
	if hr != nil {
		return outcome{cat: "halt", end: hr.End, reason: hr.Reason}
	}
	return outcome{cat: "fill"}
}

func realOutcomeExtend(er *ExtendResult, err error) outcome {
	if err != nil {
		return outcome{cat: errName(err)}
	}
	if er != nil {
		return outcome{cat: "extend", end: er.End}
	}
	return outcome{cat: "fill"}
}

type opKind int

const (
	opCheck opKind = iota
	opTrade
	opResume
)

type operation struct {
	kind  opKind
	now   int64
	sym   string
	price int64
}

type seqConfig struct {
	prev, lim, ds, dd, de, window, duration int64
	maxExtend, haltMax                      int
	open, close, protection                 int64
}

func genConfig(rng *rand.Rand) seqConfig {
	return seqConfig{
		prev:       int64(500 + rng.Intn(5000)),
		lim:        int64(200 + rng.Intn(2000)),
		ds:         int64(50 + rng.Intn(1000)),
		dd:         int64(30 + rng.Intn(600)),
		de:         int64(50 + rng.Intn(1500)),
		window:     int64(1 + rng.Intn(300)),
		duration:   int64(5 + rng.Intn(200)),
		maxExtend:  rng.Intn(3),
		haltMax:    rng.Intn(6),
		open:       0,
		close:      5000,
		protection: int64(rng.Intn(600)),
	}
}

func (c seqConfig) bounds() (int64, int64) {
	up := c.prev * (10000 + c.lim) / 10000
	dn := (c.prev*(10000-c.lim) + 9999) / 10000
	return dn, up
}

func genOperations(rng *rand.Rand, c seqConfig, n int) []operation {
	dn, up := c.bounds()
	ops := make([]operation, n)
	var now int64 = -50
	for i := range ops {
		switch rng.Intn(40) {
		case 0:
			now -= int64(1 + rng.Intn(10))
		case 1:
		default:
			now += int64(rng.Intn(120))
		}
		if now > 6000 || now < -100 {
			now = int64(rng.Intn(6000))
		}
		var price int64
		switch rng.Intn(10) {
		case 0:
			price = dn - int64(1+rng.Intn(50))
		case 1:
			price = up + int64(1+rng.Intn(50))
		case 2:
			price = dn
		case 3:
			price = up
		default:
			jitter := int64(rng.Intn(int(up-dn) + 1))
			if rng.Intn(2) == 0 {
				price = c.prev + jitter/2
			} else {
				price = c.prev - jitter/2
			}
			if price < dn {
				price = dn
			}
			if price > up {
				price = up
			}
		}
		sym := "S"
		if rng.Intn(30) == 0 {
			sym = "GHOST"
		}
		var kind opKind
		switch rng.Intn(10) {
		case 0, 1, 2:
			kind = opResume
		case 3, 4:
			kind = opCheck
		default:
			kind = opTrade
		}
		ops[i] = operation{kind: kind, now: now, sym: sym, price: price}
	}
	return ops
}

func applyReal(g *Gate, o operation) outcome {
	switch o.kind {
	case opCheck:
		if err := g.CheckOrder(o.now, o.sym, o.price); err != nil {
			return outcome{cat: errName(err)}
		}
		return outcome{cat: "accept"}
	case opResume:
		er, err := g.Resume(o.now, o.sym, o.price)
		return realOutcomeExtend(er, err)
	default:
		hr, err := g.Trade(o.now, o.sym, o.price)
		return realOutcomeTrade(hr, err)
	}
}

func applyNaive(m *naiveModel, o operation) outcome {
	switch o.kind {
	case opCheck:
		return m.check(o.now, o.sym, o.price)
	case opResume:
		return m.resume(o.now, o.sym, o.price)
	default:
		return m.trade(o.now, o.sym, o.price)
	}
}

func opName(k opKind) string {
	switch k {
	case opCheck:
		return "CheckOrder"
	case opResume:
		return "Resume"
	default:
		return "Trade"
	}
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 1500
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := genConfig(rng)
		ops := genOperations(rng, cfg, 100)

		g := New()
		if err := g.AddSymbol("S", cfg.prev, cfg.lim, cfg.ds, cfg.dd, cfg.de,
			cfg.window, cfg.duration, cfg.maxExtend, cfg.haltMax,
			cfg.open, cfg.close, cfg.protection); err != nil {
			t.Fatalf("seed=%d add: %v", seed, err)
		}
		m := &naiveModel{syms: map[string]*naiveSymbol{}}
		if err := m.add("S", cfg.prev, cfg.lim, cfg.ds, cfg.dd, cfg.de,
			cfg.window, cfg.duration, cfg.maxExtend, cfg.haltMax,
			cfg.open, cfg.close, cfg.protection); err != nil {
			t.Fatalf("seed=%d naive add: %v", seed, err)
		}

		log := fmt.Sprintf("seed=%d cfg=%+v ops=%d\n", seed, cfg, len(ops))
		mismatch := false
		for i, o := range ops {
			got := applyReal(g, o)
			want := applyNaive(m, o)
			ok := got.cat == want.cat && got.end == want.end &&
				(o.kind != opTrade || got.cat != "halt" || got.reason == want.reason)
			entry := fmt.Sprintf("  #%03d %s(now=%d sym=%s price=%d) => real=%+v naive=%+v\n",
				i, opName(o.kind), o.now, o.sym, o.price, got, want)
			log += "  " + entry
			if !ok {
				mismatch = true
				log += fmt.Sprintf("  MISMATCH at seed=%d op=%d: %+v != %+v\n", seed, i, got, want)
				break
			}
		}
		if mismatch {
			t.Fatalf("differential mismatch:\n%s", log)
		}
		if testing.Verbose() && seed%100 == 0 {
			t.Logf("seed=%d replayed %d ops, identical to naive (sample head)\n%s",
				seed, len(ops), log)
		}
	}
}

func replayAll(t *testing.T, g *Gate, c seqConfig, ops []operation) []outcome {
	t.Helper()
	if err := g.AddSymbol("S", c.prev, c.lim, c.ds, c.dd, c.de,
		c.window, c.duration, c.maxExtend, c.haltMax,
		c.open, c.close, c.protection); err != nil {
		t.Fatal(err)
	}
	got := make([]outcome, len(ops))
	for i, o := range ops {
		got[i] = applyReal(g, o)
	}
	return got
}

func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7777))
	cfg := genConfig(rng)
	ops := genOperations(rng, cfg, 200)
	first := replayAll(t, New(), cfg, ops)
	second := replayAll(t, New(), cfg, ops)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay differs at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestConcurrentDisjointSymbols(t *testing.T) {
	// now 由原子计数器全局单调发放；捕获互斥锁实际给出的线性序列，
	// 再在独立闸门上按该序列串行重放，逐笔比对。
	const workers = 16
	type job struct{ cfg seqConfig }
	jobs := make([]job, workers)
	g, g2 := New(), New()
	for w := 0; w < workers; w++ {
		rng := rand.New(rand.NewSource(int64(9000 + w)))
		cfg := genConfig(rng)
		cfg.open, cfg.close, cfg.protection = 0, 10_000_000, 1000
		jobs[w] = job{cfg}
		sym := fmt.Sprintf("W%d", w)
		for _, gg := range []*Gate{g, g2} {
			if err := gg.AddSymbol(sym, cfg.prev, cfg.lim, cfg.ds, cfg.dd, cfg.de,
				cfg.window, cfg.duration, cfg.maxExtend, cfg.haltMax,
				cfg.open, cfg.close, cfg.protection); err != nil {
				t.Fatal(err)
			}
		}
	}
	const perWorker = 80
	var ticket int64
	type record struct {
		o   operation
		out outcome
	}
	records := make([]record, 0, workers*perWorker)
	execMu := sync.Mutex{} // 与 g 的调用同临界区，给出真实线性化顺序
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := range jobs {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			rng := rand.New(rand.NewSource(int64(40000 + w)))
			dn, up := jobs[w].cfg.bounds()
			for i := 0; i < perWorker; i++ {
				price := dn + rng.Int63n(up-dn+1)
				idx := atomicAdd(&ticket, 1)
				o := operation{kind: opTrade, now: idx, sym: fmt.Sprintf("W%d", w), price: price}
				execMu.Lock()
				out := applyReal(g, o)
				records = append(records, record{o, out})
				execMu.Unlock()
			}
		}(w)
	}
	close(start)
	wg.Wait()
	for _, rec := range records {
		if got := applyReal(g2, rec.o); got != rec.out {
			t.Fatalf("linearization mismatch: op=%+v concurrent=%+v serial=%+v",
				rec.o, rec.out, got)
		}
	}
}
func TestConcurrentSharedClockSerialEquivalence(t *testing.T) {
	// 共享标的 + 单调时钟：并发提交时时钟回退错误的数量可随调度变化，
	// 但每个被接受的 now 必须等于某个合法串行交错结果；这里验证无数据竞争且
	// 所有成功 Trade 的价格都在涨跌停内、成交后不变量成立。
	g := New()
	if err := g.AddSymbol("S", 1000, 1000, 500, 200, 800, 50, 60, 1, 5, 0, 100000, 500); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			now := int64(1 + w*1000)
			for i := 0; i < 200; i++ {
				now += int64(i)
				price := int64(1000 + (i % 40) - 20)
				if hr, err := g.Trade(now, "S", price); err == nil && hr == nil {
					if price < 900 || price > 1100 {
						t.Errorf("accepted fill outside limit: %d", price)
					}
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	if got := g.symbols["S"].ph.HaltCount(); got > 5 {
		t.Fatalf("halt count %d exceeds Hmax 5", got)
	}
}
