package autoscaler

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naiveSim 是按题目规则逐步写成的朴素模拟，用于对照正式实现。
// 它不做任何并发保护，逻辑刻意写得直白。
type naiveSim struct {
	cfg     Config
	cap     int64
	batches []Batch
	b0      int64
	lastOut int64
	hasOut  bool
	lastIn  int64
	hasIn   bool
	maxNow  int64
	seen    map[int64]bool
}

func newNaiveSim(cfg Config) *naiveSim {
	return &naiveSim{cfg: cfg, cap: cfg.C0, seen: map[int64]bool{}}
}

func naivePick(steps []Step, v int64) int64 {
	pct := steps[0].Pct
	for i := 1; i < len(steps); i++ {
		if v >= steps[i].Lo {
			pct = steps[i].Pct
		}
	}
	return pct
}

func (n *naiveSim) inflight() int64 {
	total := int64(0)
	for _, b := range n.batches {
		total += b.Count
	}
	return total
}

func (n *naiveSim) result(action Action, delta int64) Result {
	return Result{Action: action, Delta: delta, Cap: n.cap, Inflight: n.inflight()}
}

// evaluate 返回结果、拒绝原因与判定依据说明。
func (n *naiveSim) evaluate(now, metric int64) (Result, error, string) {
	if now < 0 || now > 1_000_000_000_000_000 || metric < 0 || metric > 1_000_000_000 {
		return Result{}, ErrInvalidParam, "参数非法"
	}
	if now < n.maxNow {
		return Result{}, ErrClockRegression, fmt.Sprintf("时钟回退 now=%d < maxNow=%d", now, n.maxNow)
	}

	// 一：并入就绪批次。
	merged := int64(0)
	kept := n.batches[:0]
	for _, b := range n.batches {
		if b.ReadyAt <= now {
			n.cap += b.Count
			merged += b.Count
		} else {
			kept = append(kept, b)
		}
	}
	n.batches = kept

	if now > n.maxNow {
		n.maxNow = now
		n.seen = map[int64]bool{}
	}
	if n.seen[metric] {
		return n.result(ActionNone, 0), nil, "同一 now 同一 metric 重复评估"
	}
	n.seen[metric] = true

	eff := n.cap + n.inflight()

	// 二：扩容。
	if metric >= n.cfg.H {
		v := metric - n.cfg.H
		pct := naivePick(n.cfg.Up, v)
		inCooldown := n.hasOut && now < n.lastOut+n.cfg.Cout
		base := eff
		if inCooldown {
			base = n.b0
		}
		delta := (base*pct + 99) / 100
		if delta < n.cfg.Ms {
			delta = n.cfg.Ms
		}
		target := base + delta
		if target > n.cfg.Mx {
			target = n.cfg.Mx
		}
		why := fmt.Sprintf("扩容档 v=%d pct=%d base=%d(冷却内=%v) delta=%d target=%d eff=%d",
			v, pct, base, inCooldown, delta, target, eff)
		if target <= eff {
			return n.result(ActionNone, 0), nil, why + "，target<=eff 无动作"
		}
		n.batches = append(n.batches, Batch{ReadyAt: now + n.cfg.W, Count: target - eff})
		if !inCooldown {
			n.b0 = eff
			n.lastOut = now
			n.hasOut = true
		}
		return n.result(ActionScaleOut, target-eff), nil, why + "，追加在途批次"
	}

	// 三：缩容。
	if metric < n.cfg.Lw {
		u := n.cfg.Lw - metric
		pct := naivePick(n.cfg.Down, u)
		if len(n.batches) > 0 {
			return n.result(ActionNone, 0), nil, "存在在途批次，缩容被阻止"
		}
		if n.hasIn && now < n.lastIn+n.cfg.Cin {
			return n.result(ActionNone, 0), nil, "缩容冷却内，无动作"
		}
		delta := n.cap * pct / 100
		if delta < 1 {
			delta = 1
		}
		target := n.cap - delta
		if target < n.cfg.Mn {
			target = n.cfg.Mn
		}
		why := fmt.Sprintf("缩容档 u=%d pct=%d delta=%d target=%d cap=%d", u, pct, delta, target, n.cap)
		if target >= n.cap {
			return n.result(ActionNone, 0), nil, why + "，target>=cap 无动作"
		}
		removed := n.cap - target
		n.cap = target
		n.lastIn = now
		n.hasIn = true
		return n.result(ActionScaleIn, removed), nil, why + "，执行缩容"
	}

	// 四：死区。
	return n.result(ActionNone, 0), nil, fmt.Sprintf("metric 位于 [Lw,H) 死区（并入 %d）", merged)
}

func (n *naiveSim) snapshot() Snapshot {
	batches := make([]Batch, len(n.batches))
	copy(batches, n.batches)
	return Snapshot{
		Cap: n.cap, Batches: batches, B0: n.b0,
		LastOut: n.lastOut, HasLastOut: n.hasOut,
		LastIn: n.lastIn, HasLastIn: n.hasIn,
		MaxNow: n.maxNow,
	}
}

func randSteps(r *rand.Rand) []Step {
	n := 1 + r.Intn(4)
	steps := make([]Step, n)
	lo := int64(0)
	for i := range steps {
		if i > 0 {
			lo += 1 + r.Int63n(20)
		}
		steps[i] = Step{Lo: lo, Pct: 1 + r.Int63n(1000)}
	}
	return steps
}

func randConfig(r *rand.Rand) Config {
	mn := 1 + r.Int63n(50)
	mx := mn + r.Int63n(200)
	lw := r.Int63n(400)
	h := lw + 1 + r.Int63n(400)
	return Config{
		Mn: mn, Mx: mx, C0: mn + r.Int63n(mx-mn+1),
		H: h, Lw: lw,
		Up: randSteps(r), Down: randSteps(r),
		Ms: 1 + r.Int63n(20), W: 1 + r.Int63n(8),
		Cout: r.Int63n(12), Cin: r.Int63n(12),
	}
}

func randMetric(r *rand.Rand, cfg Config) int64 {
	switch r.Intn(10) {
	case 0, 1: // H 边界附近
		m := cfg.H - 3 + r.Int63n(7)
		if m < 0 {
			m = 0
		}
		return m
	case 2, 3: // Lw 边界附近
		m := cfg.Lw - 3 + r.Int63n(7)
		if m < 0 {
			m = 0
		}
		return m
	case 4:
		return 0
	case 5:
		return 1_000_000_000
	default:
		return r.Int63n(2*cfg.H + 10)
	}
}

// 2000 组随机评估序列与朴素模拟逐步对照，日志打印输入、输出与判定依据。
func TestRandomAgainstNaiveSim(t *testing.T) {
	r := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		cfg := randConfig(r)
		s, err := NewScaler(cfg)
		if err != nil {
			t.Fatalf("seq %d: NewScaler(%+v): %v", seq, cfg, err)
		}
		sim := newNaiveSim(cfg)
		ops := 5 + r.Intn(36)
		now := int64(0)
		for op := 0; op < ops; op++ {
			var metric int64
			switch r.Intn(100) {
			case 0: // 非法参数
				if r.Intn(2) == 0 {
					now, metric = -1, randMetric(r, cfg)
				} else {
					metric = 1_000_000_001
				}
			case 1: // 时钟回退
				if now > 0 {
					now--
				}
				metric = randMetric(r, cfg)
			default:
				if r.Intn(10) < 4 {
					now += r.Int63n(4) // 小步推进，制造冷却/同 now 重复
				} else {
					now += r.Int63n(30)
				}
				metric = randMetric(r, cfg)
			}

			gotRes, gotErr := s.Evaluate(now, metric)
			wantRes, wantErr, why := sim.evaluate(now, metric)
			t.Logf("seq=%d op=%d Evaluate(now=%d, metric=%d) => res=%+v err=%v 判定依据: %s",
				seq, op, now, metric, gotRes, gotErr, why)

			if gotErr != wantErr {
				t.Fatalf("seq %d op %d Evaluate(%d,%d): err = %v, want %v (cfg=%+v)",
					seq, op, now, metric, gotErr, wantErr, cfg)
			}
			if gotErr == nil && gotRes != wantRes {
				t.Fatalf("seq %d op %d Evaluate(%d,%d): res = %+v, want %+v (cfg=%+v)",
					seq, op, now, metric, gotRes, wantRes, cfg)
			}
			if gotSnap, wantSnap := s.Snapshot(), sim.snapshot(); !reflect.DeepEqual(gotSnap, wantSnap) {
				t.Fatalf("seq %d op %d Evaluate(%d,%d): state mismatch\ngot  %+v\nwant %+v\n(cfg=%+v)",
					seq, op, now, metric, gotSnap, wantSnap, cfg)
			}

			// 不变量检查。
			snap := s.Snapshot()
			if gotErr == nil {
				if snap.Cap < cfg.Mn || snap.Cap > cfg.Mx {
					t.Fatalf("seq %d op %d: cap %d out of [%d,%d]", seq, op, snap.Cap, cfg.Mn, cfg.Mx)
				}
				if snap.Eff() > cfg.Mx {
					t.Fatalf("seq %d op %d: eff %d exceeds Mx %d", seq, op, snap.Eff(), cfg.Mx)
				}
				for _, b := range snap.Batches {
					if b.ReadyAt <= snap.MaxNow {
						t.Fatalf("seq %d op %d: batch readyAt %d <= maxNow %d", seq, op, b.ReadyAt, snap.MaxNow)
					}
				}
			}
		}
	}
}
