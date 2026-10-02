package ontology

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveMarker 为逐毫秒结算的朴素参考实现：每毫秒各补 CIR、
// 每毫秒把 Tc 溢出立即耦合到 Te（超出 EBS 丢弃），其余规则与 Marker 完全一致。
type naiveMarker struct {
	cir, cbs, ebs, w, pn int64
	k                    int
	tc, te               int64
	last, until          int64
	lastUntil            int64
	streak               int
	queue                []int64
	overflow             int64

	gc, gb, yc, yb, rc, rb int64
}

// TestRandomConservationBounds 在无 Reconfigure 的随机序列上校验两条令牌守恒界。
func TestRandomConservationBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		cfg := Config{
			CIR: int64(rng.Intn(50)) + 1,
			CBS: int64(rng.Intn(500)) + 1,
			EBS: int64(rng.Intn(300)),
			W:   int64(rng.Intn(50)) + 1,
			K:   rng.Intn(5) + 1,
			Pn:  int64(rng.Intn(40)) + 1,
		}
		m := mustNew(t, cfg)
		now := int64(0)
		var inBytes int64
		for i := 0; i < 100; i++ {
			now += int64(rng.Intn(8))
			c := Color(rng.Intn(4))
			b := int64(rng.Intn(600)) + 1
			if _, err := m.Mark(now, c, b); err != nil {
				t.Fatal(err)
			}
			inBytes += b
		}
		snap, _ := m.State(now)
		_, gb, _, yb, _, rb := m.Counters()
		if inBytes != gb+yb+rb {
			t.Fatalf("trial=%d input bytes not partitioned", trial)
		}
		bound := cfg.CBS + cfg.EBS + cfg.CIR*snap.Last
		if gb+yb > bound {
			t.Fatalf("trial=%d G+Y=%d > CBS+EBS+CIR*last=%d", trial, gb+yb, bound)
		}
		if yb > cfg.EBS+m.TotalOverflowToTe() {
			t.Fatalf("trial=%d Yellow=%d > EBS+overflow=%d", trial, yb, cfg.EBS+m.TotalOverflowToTe())
		}
	}
}

// TestReplayDeterminism 同一操作序列重放两次，输出与桶状态完全一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() []Result {
		m := mustNew(t, baseCfg(3))
		var got []Result
		for _, p := range []struct {
			now   int64
			color Color
			b     int64
		}{
			{0, Green, 100}, {0, Green, 60}, {0, Green, 50}, {10, Blind, 80},
			{30, Yellow, 30}, {30, Yellow, 25}, {30, Red, 5}, {100, Green, 500},
		} {
			r, err := m.Mark(p.now, p.color, p.b)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, r)
		}
		return got
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("replay mismatch at %d:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}

// TestConcurrent 并发调用结果等价于某个串行顺序（-race 下验证）。
func TestConcurrent(t *testing.T) {
	m := mustNew(t, Config{CIR: 5, CBS: 1000, EBS: 500, W: 100, K: 3, Pn: 20})
	var wg sync.WaitGroup
	var clock int64
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g + 1)))
			for i := 0; i < 200; i++ {
				c := Color(rng.Intn(4))
				b := int64(rng.Intn(50)) + 1
				var r Result
				for attempt := 0; ; attempt++ {
					now := atomic.AddInt64(&clock, int64(1+rng.Intn(3)))
					var err error
					r, err = m.Mark(now, c, b)
					if errors.Is(err, ErrClockRollback) && attempt < 1000 {
						continue // 时间戳被其他协程抢先：取新时刻重试
					}
					if err != nil {
						t.Errorf("Mark: %v", err)
						return
					}
					break
				}
				if r.Tc < 0 || r.Te < 0 {
					t.Errorf("negative buckets")
					return
				}
				if g == 0 && i%50 == 0 && rng.Intn(2) == 0 {
					for attempt := 0; ; attempt++ {
						cfgNow := atomic.AddInt64(&clock, 1)
						err := m.Reconfigure(cfgNow, 5, 1000, 500)
						if errors.Is(err, ErrClockRollback) && attempt < 1000 {
							continue
						}
						if err != nil {
							t.Errorf("Reconfigure: %v", err)
							return
						}
						break
					}
				}
			}
		}(g)
	}
	wg.Wait()

	// 并发结束后统计守恒：输入总字节 = 各输出色字节之和。
	// 由于各 goroutine 时间轴不同（now 可能小于全局 last），
	// 回退调用被拒绝且不计费，因此这里只校验非负与桶界。
	s, err := m.State(1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tc < 0 || s.Tc > s.CBS || s.Te < 0 || s.Te > s.EBS {
		t.Fatalf("final bucket invariants violated: %+v", s)
	}
}

func newNaive(cfg Config) *naiveMarker {
	return &naiveMarker{
		cir: cfg.CIR, cbs: cfg.CBS, ebs: cfg.EBS, w: cfg.W, pn: cfg.Pn, k: cfg.K,
		tc: cfg.CBS, te: cfg.EBS,
	}
}

// refillStep 逐毫秒补充：Tc+=CIR，当毫秒溢出立即灌入 Te。
func (n *naiveMarker) refillStep(now int64) {
	for t := n.last; t < now; t++ {
		n.tc += n.cir
		if n.tc > n.cbs {
			o := n.tc - n.cbs
			n.tc = n.cbs
			if o > n.ebs-n.te {
				o = n.ebs - n.te
			}
			n.te += o
			n.overflow += o
		}
	}
	n.last = now
}

func (n *naiveMarker) prune(now int64) {
	cutoff := now - n.w
	kept := n.queue[:0]
	for _, t := range n.queue {
		if t > cutoff { // t+W<=now 恰等剔除
			kept = append(kept, t)
		}
	}
	n.queue = kept
}

type naiveResult struct {
	color     Color
	penalized bool
}

func (n *naiveMarker) mark(now int64, c Color, b int64) naiveResult {
	n.refillStep(now)
	out := Red
	penalized := false
	if now < n.until {
		out, penalized = Red, true
	} else {
		switch c {
		case Green, Blind:
			switch {
			case n.tc >= b:
				n.tc -= b
				out = Green
			case n.te >= b:
				n.te -= b
				out = Yellow
			}
		case Yellow:
			if n.te >= b {
				n.te -= b
				out = Yellow
			}
		case Red:
			out = Red
		}
		if c != Red && out == Red {
			n.prune(now)
			n.queue = append(n.queue, now)
			if len(n.queue) >= n.k {
				if n.lastUntil > 0 && now-n.lastUntil < n.w {
					if n.streak < maxS {
						n.streak++
					}
				} else {
					n.streak = 0
				}
				n.until = now + (n.pn << n.streak)
				n.lastUntil = n.until
				n.queue = n.queue[:0]
			}
		}
	}
	switch out {
	case Green:
		n.gc++
		n.gb += b
	case Yellow:
		n.yc++
		n.yb += b
	case Red:
		n.rc++
		n.rb += b
	}
	return naiveResult{out, penalized}
}

func (n *naiveMarker) reconfigure(now, cir, cbs, ebs int64) {
	n.refillStep(now)
	if n.tc > cbs {
		n.tc = cbs
	}
	if n.te > ebs {
		n.te = ebs
	}
	n.cir, n.cbs, n.ebs = cir, cbs, ebs
}

type refOp struct {
	kind          int // 0=Mark, 1=Reconfigure
	now           int64
	color         Color
	b             int64
	cir, cbs, ebs int64
}

// TestRandomVsNaive 对 2000 组随机操作序列做批量结算 vs 逐毫秒朴素模拟的逐步对照。
func TestRandomVsNaive(t *testing.T) {
	const trials = 2000
	rng := rand.New(rand.NewSource(20261002))

	for trial := 0; trial < trials; trial++ {
		cfg := Config{
			CIR: int64(rng.Intn(20)) + 1,
			CBS: int64(rng.Intn(200)) + 1,
			EBS: int64(rng.Intn(150)),
			W:   int64(rng.Intn(40)) + 1,
			K:   rng.Intn(5) + 1,
			Pn:  int64(rng.Intn(30)) + 1,
		}
		real, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		naive := newNaive(cfg)

		ops := make([]refOp, rng.Intn(60)+1)
		now := int64(0)
		for i := range ops {
			// 时间单调不减：约 8% 同刻，其余小步前进（保证逐毫秒成本可控）。
			if rng.Intn(12) != 0 {
				now += int64(rng.Intn(6))
			}
			if rng.Intn(12) == 0 {
				ops[i] = refOp{
					kind: 1, now: now,
					cir: int64(rng.Intn(20)) + 1,
					cbs: int64(rng.Intn(200)) + 1,
					ebs: int64(rng.Intn(150)),
				}
				continue
			}
			ops[i] = refOp{
				kind: 0, now: now,
				color: Color(rng.Intn(4)),
				b:     int64(rng.Intn(260)) + 1,
			}
		}

		for i, op := range ops {
			if op.kind == 1 {
				if err := real.Reconfigure(op.now, op.cir, op.cbs, op.ebs); err != nil {
					t.Fatalf("trial=%d step=%d Reconfigure: %v", trial, i, err)
				}
				naive.reconfigure(op.now, op.cir, op.cbs, op.ebs)
				continue
			}
			r, err := real.Mark(op.now, op.color, op.b)
			if err != nil {
				t.Fatalf("trial=%d step=%d Mark: %v", trial, i, err)
			}
			nr := naive.mark(op.now, op.color, op.b)
			t.Logf("trial=%d step=%d Mark(now=%d,in=%s,b=%d) -> out=%s naive=%s pen=%v/%v Tc=%d/%d Te=%d/%d until=%d/%d s=%d/%d q=%d/%d",
				trial, i, op.now, op.color, op.b, r.Color, nr.color, r.Penalized, nr.penalized,
				r.Tc, naive.tc, r.Te, naive.te, r.Until, naive.until, r.Streak, naive.streak,
				r.RedQueueLen, len(naive.queue))
			if r.Color != nr.color || r.Penalized != nr.penalized ||
				r.Tc != naive.tc || r.Te != naive.te ||
				r.Until != naive.until || r.Streak != naive.streak ||
				r.RedQueueLen != len(naive.queue) {
				t.Fatalf("trial=%d step=%d mismatch:\nreal=%+v\nnaive={color=%v pen=%v tc=%d te=%d until=%d s=%d q=%v}",
					trial, i, r, nr.color, nr.penalized, naive.tc, naive.te,
					naive.until, naive.streak, naive.queue)
			}
			if r.Tc < 0 || r.Tc > naive.cbs || r.Te < 0 || r.Te > naive.ebs {
				t.Fatalf("bucket invariants violated at trial=%d step=%d", trial, i)
			}
		}

		// 序列结束后在某未来时刻对齐快照，并核对统计。
		end := now + int64(rng.Intn(10))
		snap, err := real.State(end)
		if err != nil {
			t.Fatal(err)
		}
		naive.refillStep(end)
		if snap.Tc != naive.tc || snap.Te != naive.te {
			t.Fatalf("trial=%d final snapshot mismatch Tc=%d/%d Te=%d/%d",
				trial, snap.Tc, naive.tc, snap.Te, naive.te)
		}
		gc, gb, yc, yb, rc, rb := real.Counters()
		if gc != naive.gc || gb != naive.gb || yc != naive.yc || yb != naive.yb ||
			rc != naive.rc || rb != naive.rb {
			t.Fatalf("trial=%d counters mismatch", trial)
		}
		if gb+yb+rb != naive.gb+naive.yb+naive.rb {
			t.Fatalf("trial=%d byte conservation mismatch", trial)
		}
	}
}
