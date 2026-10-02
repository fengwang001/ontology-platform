package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// naiveSim 是按题面规则逐步重写的朴素参考实现：每次重新求和窗口。
type naiveSim struct {
	cfg     Config
	cur     uint64
	ch      int
	win     [][2]uint64 // {bytes, dt}
	streak  int
	pol     bool
	paused  bool
	lastDir Direction
	gap     int
	damp    int
	applied int64
	forced  int64
	skipped int64
	winOps  int64
	log     []string
}

func newNaive(cfg Config) *naiveSim {
	return &naiveSim{cfg: cfg, cur: cfg.B0, ch: cfg.C0, lastDir: DirNone}
}

func nbeff(cfg Config, c int) uint64 {
	v := cfg.Pool / (uint64(c) * cfg.G) * cfg.G
	if v > cfg.Bmax {
		return cfg.Bmax
	}
	return v
}

func (n *naiveSim) sample(bytes, dt uint64) SampleResult {
	if bytes > 1<<30 || dt < 1 || dt > 1_000_000 {
		n.log = append(n.log, fmt.Sprintf("S(%d,%d) REJECT invalid", bytes, dt))
		return SampleResult{Action: ActionRejected, Cur: n.cur}
	}
	if n.paused {
		n.log = append(n.log, fmt.Sprintf("S(%d,%d) REJECT paused", bytes, dt))
		return SampleResult{Action: ActionRejected, Cur: n.cur}
	}
	if n.pol {
		n.pol = false
		n.skipped++
		n.log = append(n.log, fmt.Sprintf("S(%d,%d) SKIPPED (cur=%d)", bytes, dt, n.cur))
		return SampleResult{Action: ActionSkipped, Cur: n.cur, Streak: n.streak}
	}

	if n.lastDir != DirNone {
		n.gap++
	}
	n.win = append(n.win, [2]uint64{bytes, dt})
	n.winOps++
	if len(n.win) > n.cfg.W {
		n.win = n.win[1:]
		n.winOps++
	}

	var sb, sd uint64
	for _, s := range n.win {
		sb += s[0]
		sd += s[1]
	}
	r := (sb * 1000) / sd
	dtgt := (r*n.cfg.T + 999) / 1000
	per := (dtgt + uint64(n.ch) - 1) / uint64(n.ch)
	eff := nbeff(n.cfg, n.ch)
	raw := per
	if raw < n.cfg.Bmin {
		raw = n.cfg.Bmin
	}
	if raw > eff {
		raw = eff
	}
	cand := raw / n.cfg.G * n.cfg.G

	startDamp := n.damp
	setDamp := false
	res := SampleResult{Cur: n.cur, R: r, Cand: cand}

	if cand == n.cur {
		n.streak = 0
		res.Action = ActionHold
	} else if cand > n.cur {
		if (cand-n.cur)*100 >= n.cur*n.cfg.ThU || cand == eff {
			n.streak++
			need := n.cfg.Kc
			if startDamp > 0 {
				need = 2 * n.cfg.Kc
			}
			if n.streak >= need {
				n.cur = cand
				n.streak = 0
				n.pol = true
				n.applied++
				setDamp = n.change(DirUp)
				res.Action = ActionApplied
			} else {
				res.Action = ActionPending
			}
			res.Streak = n.streak
		} else {
			n.streak = 0
			res.Action = ActionHold
		}
	} else {
		if (n.cur-cand)*100 >= n.cur*n.cfg.ThD || cand == n.cfg.Bmin {
			n.cur = cand
			n.streak = 0
			n.pol = true
			n.applied++
			setDamp = n.change(DirDown)
			res.Action = ActionApplied
			res.Streak = 0
		} else {
			n.streak = 0
			res.Action = ActionHold
		}
	}
	if n.damp > 0 && !setDamp {
		n.damp--
	}
	res.Cur = n.cur
	n.log = append(n.log, fmt.Sprintf(
		"S(%d,%d) %s R=%d cand=%d cur=%d streak=%d gap=%d damp=%d pol=%v | need-basis damp0=%v",
		bytes, dt, res.Action, r, cand, n.cur, n.streak, n.gap, n.damp, n.pol, startDamp > 0))
	return res
}

func (n *naiveSim) change(dir Direction) bool {
	set := false
	if n.lastDir != DirNone && dir != n.lastDir && n.gap <= n.cfg.H {
		if n.cfg.H > 0 {
			n.damp = n.cfg.H
			set = true
		}
	}
	n.lastDir = dir
	n.gap = 0
	return set
}

func (n *naiveSim) setChannels(c int) (Action, uint64) {
	if c < 1 || c > 10000 {
		n.log = append(n.log, fmt.Sprintf("C(%d) REJECT invalid", c))
		return ActionRejected, n.cur
	}
	if c == n.ch {
		n.log = append(n.log, fmt.Sprintf("C(%d) NOOP", c))
		return ActionNoop, n.cur
	}
	eff := nbeff(n.cfg, c)
	if eff < n.cfg.Bmin {
		n.log = append(n.log, fmt.Sprintf("C(%d) REJECT capacity eff=%d", c, eff))
		return ActionRejected, n.cur
	}
	n.ch = c
	n.streak = 0
	if n.cur > eff {
		n.cur = eff
		n.pol = true
		n.forced++
		n.change(DirDown)
		n.log = append(n.log, fmt.Sprintf("C(%d) FORCED cur=%d gap=%d damp=%d", c, n.cur, n.gap, n.damp))
		return ActionForced, n.cur
	}
	n.log = append(n.log, fmt.Sprintf("C(%d) OK cur=%d", c, n.cur))
	return ActionOK, n.cur
}

func (n *naiveSim) resume() {
	n.paused = false
	n.win = nil
	n.streak = 0
	n.log = append(n.log, "RESUME window cleared")
}

type op struct {
	kind  int // 0 sample, 1 setch, 2 pause, 3 resume
	bytes uint64
	dt    uint64
	c     int
}

func TestRandomNaiveComparison2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < 2000; iter++ {
		g := uint64([]int{1, 64, 1024}[rng.Intn(3)])
		cfg := Config{
			G:    g,
			Bmin: g * uint64(1+rng.Intn(8)),
			Bmax: g * uint64(64+rng.Intn(1024)),
			T:    1 + uint64(rng.Intn(100000)),
			W:    1 + rng.Intn(100),
			ThU:  uint64(rng.Intn(1001)),
			ThD:  uint64(rng.Intn(101)),
			Kc:   1 + rng.Intn(10),
			C0:   1 + rng.Intn(64),
			H:    rng.Intn(12),
		}
		cfg.B0 = cfg.Bmin + g*uint64(rng.Intn(int((cfg.Bmax-cfg.Bmin)/g+1)))
		// Pool：保证 Beff(C0)>=B0，同时允许更大通道数时容量不足
		cfg.Pool = cfg.B0 * uint64(cfg.C0) * uint64(1+rng.Intn(4))

		d, err := NewDeflator(cfg)
		if err != nil {
			t.Fatalf("iter %d valid cfg rejected: %+v err=%v", iter, cfg, err)
		}
		n := newNaive(cfg)

		nops := 30 + rng.Intn(120)
		var ops []op
		for i := 0; i < nops; i++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5, 6:
				// 偶尔越界，测试拒绝路径
				var b, dt uint64
				if rng.Intn(20) == 0 {
					b = 1<<30 + 1
				} else {
					b = uint64(rng.Int63n(1 << 30))
				}
				if rng.Intn(20) == 0 {
					dt = 0
				} else {
					dt = 1 + uint64(rng.Intn(10000))
				}
				ops = append(ops, op{kind: 0, bytes: b, dt: dt})
			case 7:
				c := 1 + rng.Intn(128)
				if rng.Intn(15) == 0 {
					c = 10001
				}
				ops = append(ops, op{kind: 1, c: c})
			case 8:
				ops = append(ops, op{kind: 2})
			default:
				ops = append(ops, op{kind: 3})
			}
		}

		for oi, o := range ops {
			switch o.kind {
			case 0:
				got, gerr := d.Sample(o.bytes, o.dt)
				want := n.sample(o.bytes, o.dt)
				rejected := gerr != nil
				if rejected != (want.Action == ActionRejected) {
					dumpFailure(t, iter, oi, cfg, ops[:oi+1], n, got, want)
				}
				if !rejected && (got.Action != want.Action || got.Cur != want.Cur ||
					got.R != want.R || got.Cand != want.Cand || got.Streak != want.Streak) {
					dumpFailure(t, iter, oi, cfg, ops[:oi+1], n, got, want)
				}
			case 1:
				ga, gcur, gerr := d.SetChannels(o.c)
				wa, wcur := n.setChannels(o.c)
				rejected := gerr != nil
				if rejected != (wa == ActionRejected) {
					dumpFailure(t, iter, oi, cfg, ops[:oi+1], n,
						SampleResult{Action: ga, Cur: gcur}, SampleResult{Action: wa, Cur: wcur})
				}
				if !rejected && (ga != wa || gcur != wcur) {
					dumpFailure(t, iter, oi, cfg, ops[:oi+1], n,
						SampleResult{Action: ga, Cur: gcur}, SampleResult{Action: wa, Cur: wcur})
				}
			case 2:
				d.Pause()
				n.paused = true
			case 3:
				d.Resume()
				n.resume()
			}
			compareStates(t, iter, oi, d, n)
		}
	}
}

func compareStates(t *testing.T, iter, oi int, d *Deflator, n *naiveSim) {
	t.Helper()
	s := d.State()
	winLen := len(n.win)
	if s.Cur != n.cur || s.Channels != n.ch || s.WindowLen != winLen ||
		s.Streak != n.streak || s.Polluted != n.pol || s.Paused != n.paused ||
		s.LastDir != n.lastDir || s.Gap != n.gap || s.Damp != n.damp ||
		s.AppliedCount != n.applied || s.ForcedCount != n.forced ||
		s.SkippedCount != n.skipped || s.WindowOps != n.winOps {
		for _, l := range n.log {
			t.Log(l)
		}
		t.Fatalf("iter %d op %d state mismatch:\n got %+v\nwant cur=%d ch=%d wlen=%d streak=%d pol=%v paused=%v dir=%v gap=%d damp=%d applied=%d forced=%d skipped=%d winops=%d",
			iter, oi, s, n.cur, n.ch, winLen, n.streak, n.pol, n.paused, n.lastDir,
			n.gap, n.damp, n.applied, n.forced, n.skipped, n.winOps)
	}
	for i, sm := range n.win {
		if s.WindowBytes[i] != sm[0] || s.WindowDt[i] != sm[1] {
			t.Fatalf("iter %d op %d window content mismatch at %d", iter, oi, i)
		}
	}
}

func dumpFailure(t *testing.T, iter, oi int, cfg Config, done []op, n *naiveSim, got, want SampleResult) {
	t.Helper()
	for _, l := range n.log {
		t.Log(l)
	}
	t.Fatalf("iter %d op %d mismatch\n cfg=%+v\n got=%+v\nwant=%+v", iter, oi, cfg, got, want)
}
