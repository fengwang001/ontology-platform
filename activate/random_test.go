package activate

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/roster"
)

type opKind int

const (
	kActivate opKind = iota
	kReset
	kDeactivate
	kRegister
)

type op struct {
	kind  opKind
	sn    Sn
	fp    Fp
	now   int64
	batch Batch
	until int64
	sns   []Sn
}

type genCfg struct {
	m, n int
	lk   int64
}

type outcome struct {
	err string
	res Result
}

func genOps(rng *rand.Rand) (genCfg, []op) {
	cfg := genCfg{m: 1 + rng.Intn(4), n: 1 + rng.Intn(4), lk: int64(1 + rng.Intn(200))}
	pool := []Sn{"A", "B", "C", "D", "E", "F", "G", "H"}
	n0 := 1 + rng.Intn(5)
	until0 := int64(200 + rng.Intn(1000))
	ops := []op{{kind: kRegister, batch: "b0", until: until0, sns: pool[:n0], now: 0}}
	now := int64(0)
	for i := 0; i < 40+rng.Intn(40); i++ {
		now += int64(rng.Intn(61))
		t := now
		if rng.Intn(8) == 0 {
			t -= int64(1 + rng.Intn(10))
			if t < 0 {
				t = 0
			}
		}
		sn := pool[rng.Intn(n0)]
		if rng.Intn(12) == 0 {
			sn = "Z"
		}
		switch rng.Intn(10) {
		case 0, 1:
			ops = append(ops, op{kind: kReset, sn: sn, now: t})
		case 2, 3:
			ops = append(ops, op{kind: kDeactivate, sn: sn, now: t})
		default:
			ops = append(ops, op{kind: kActivate, sn: sn,
				fp: Fp([]byte{'f', byte('1' + rng.Intn(4))}), now: t})
		}
	}
	return cfg, ops
}

func runOnModel(cfg genCfg, ops []op) []outcome {
	mm := newModel(cfg.m, cfg.n, cfg.lk)
	mm.quota["t"] = cfg.n
	mm.used["t"] = 0
	outs := make([]outcome, 0, len(ops))
	for _, o := range ops {
		var res Result
		var err error
		switch o.kind {
		case kActivate:
			res, err, _ = mm.activate(o.sn, o.fp, o.now)
		case kReset:
			err, _ = mm.reset(o.sn, o.now)
		case kDeactivate:
			err, _ = mm.deactivate(o.sn, o.now)
		case kRegister:
			err, _ = mm.register(o.batch, "t", o.until, o.sns, o.now)
		}
		outs = append(outs, outcome{errName(err), res})
	}
	return outs
}

func runOnSvc(t *testing.T, cfg genCfg, ops []op, verbose bool) []outcome {
	t.Helper()
	s := newSvc(t, cfg.m, cfg.lk)
	if err := s.AddTenant("t", cfg.n); err != nil {
		t.Fatal(err)
	}
	outs := make([]outcome, 0, len(ops))
	for i, o := range ops {
		var res Result
		var err error
		var basis string
		label := "ACT"
		switch o.kind {
		case kActivate:
			res, err = s.Activate(o.sn, o.fp, o.now)
			basis = svcStateLine(s, o.sn)
		case kReset:
			label = "RST"
			err = s.Reset(o.sn, o.now)
			basis = svcStateLine(s, o.sn)
		case kDeactivate:
			label = "DEA"
			err = s.Deactivate(o.sn, o.now)
			basis = svcStateLine(s, o.sn)
		case kRegister:
			label = "REG"
			err = s.RegisterBatch(o.batch, "t", o.until, o.sns, o.now)
			basis = "register all-or-nothing"
		}
		if verbose {
			t.Logf("[%03d] %s sn=%s fp=%q now=%d -> %s res={%d,%d} | %s",
				i, label, o.sn, o.fp, o.now, errName(err), res.ID, res.Gen, basis)
		}
		outs = append(outs, outcome{errName(err), res})
	}
	return outs
}

func TestRandomVsModel1500(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(20261004))
	for seq := 0; seq < 1500; seq++ {
		cfg, ops := genOps(rng)
		want := runOnModel(cfg, ops)
		got := runOnSvc(t, cfg, ops, seq%150 == 0)
		for i := range want {
			if want[i] != got[i] {
				runOnSvc(t, cfg, ops, true)
				for j, o := range ops {
					t.Logf("seq=%d op%02d kind=%d sn=%s fp=%q now=%d", seq, j, o.kind, o.sn, o.fp, o.now)
				}
				t.Fatalf("seq=%d step=%d want=%+v got=%+v (cfg=%+v)", seq, i, want[i], got[i], cfg)
			}
		}
	}
}

func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	cfg, ops := genOps(rng)
	r1 := runOnSvc(t, cfg, ops, false)
	r2 := runOnSvc(t, cfg, ops, false)
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, r1[i], r2[i])
		}
	}
}

func TestConcurrentFirstActivateAndQuota(t *testing.T) {
	// 不变量：同 sn 并发首次激活恰有一个指纹绑定成功；同指纹全部拿到幂等结果，
	// 异指纹全部 ErrConflict；租户 used 任何时刻不超过 N；id 连续。
	s := newSvc(t, 5, 100)
	_ = s.AddTenant("t", 4)
	_ = s.AddTenant("t2", 1)
	sns := []Sn{"A", "B", "C", "D"}
	_ = s.RegisterBatch("b", "t", 1_000_000_000, sns, 0)
	_ = s.RegisterBatch("b2", "t2", 1_000_000_000, []Sn{"E", "F"}, 0)

	var wg sync.WaitGroup
	for _, sn := range sns[:4] {
		for k := 0; k < 6; k++ {
			wg.Add(1)
			go func(sn Sn, k int) {
				defer wg.Done()
				r, err := s.Activate(sn, "same", 10+int64(k%3))
				if err == nil && r.Gen != 1 {
					t.Errorf("bad gen %s: %+v", sn, r)
				}
			}(sn, k)
		}
	}
	wg.Wait()
	if s.used["t"] != 4 {
		t.Fatalf("used must be 4, got %d", s.used["t"])
	}

	// 全新 sn E（独立租户 N=1），分两阶段，同一 now 消除时钟回退噪声：
	// 阶段一：8 个同指纹并发首激活——恰一次绑定，其余全部拿到相同幂等结果。
	// 阶段二：已激活态下 4 个异指纹 + 4 个同指纹并发——异指纹全 ErrConflict
	// （M=5 > 4，不锁定），同指纹全幂等。
	sn := Sn("E")
	var mu sync.Mutex
	success := map[Result]int{}
	var conflicts int
	doActivate := func(wg *sync.WaitGroup, fp Fp, now int64) {
		defer wg.Done()
		r, err := s.Activate(sn, fp, now)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			success[r]++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Errorf("unexpected err for fp=%q: %v", fp, err)
		}
		used := s.used["t"] + s.used["t2"]
		if s.used["t2"] > 1 || used > 5 {
			t.Errorf("t2 quota exceeded: %d", s.used["t2"])
		}
	}
	var wg1 sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg1.Add(1)
		go doActivate(&wg1, "win", 100)
	}
	wg1.Wait()
	if len(success) != 1 {
		t.Fatalf("phase1: exactly one result, got %+v", success)
	}
	var win Result
	for r, c := range success {
		win = r
		if c != 8 {
			t.Fatalf("phase1: 8 same-fp goroutines succeed, got %d", c)
		}
	}

	var wg2 sync.WaitGroup
	success = map[Result]int{}
	conflicts = 0
	for i := 0; i < 4; i++ {
		wg2.Add(1)
		go doActivate(&wg2, "win", 101)
	}
	for i := 0; i < 4; i++ {
		wg2.Add(1)
		fp := Fp(fmt.Sprintf("x%d", i))
		go doActivate(&wg2, fp, 101)
	}
	wg2.Wait()
	if len(success) != 1 {
		t.Fatalf("phase2: exactly one replay result, got %+v", success)
	}
	for r, c := range success {
		if r != win || c != 4 {
			t.Fatalf("phase2: 4 same-fp replays of %+v, got %+v x%d", win, r, c)
		}
	}
	if conflicts != 4 {
		t.Fatalf("phase2: 4 different-fp conflicts, got %d", conflicts)
	}
	rec, _ := s.rs.Snapshot(sn)
	if rec.Gen != 1 || rec.State != roster.Activated {
		t.Fatalf("winner record: %+v", rec)
	}
	if r, err := s.Activate(sn, rec.Fp, 101); err != nil || r != win {
		t.Fatalf("replay winner fp: %+v %v", r, err)
	}
	if s.used["t"] != 4 || s.used["t2"] != 1 {
		t.Fatalf("used t=%d t2=%d", s.used["t"], s.used["t2"])
	}
}

func TestActivateProbeBudget(t *testing.T) {
	for _, n := range []int{100, 10_000} {
		s := newSvc(t, 2, 100)
		_ = s.AddTenant("t", n)
		sns := make([]Sn, n)
		for i := range sns {
			sns[i] = Sn(fmt.Sprintf("s%05d", i))
		}
		if err := s.RegisterBatch("b", "t", 1_000_000_000, sns, 0); err != nil {
			t.Fatal(err)
		}
		target := sns[n-1]
		before := s.rs.Probes()
		if _, err := s.Activate(target, "f", 10); err != nil {
			t.Fatal(err)
		}
		delta := s.rs.Probes() - before
		if delta > 2 {
			t.Fatalf("n=%d activate probes=%d, budget=2", n, delta)
		}
		t.Logf("n=%d single Activate roster probes=%d (quota check O(1))", n, delta)
	}
}
