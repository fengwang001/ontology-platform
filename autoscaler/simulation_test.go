package autoscaler

// 本文件包含一个按题目规则逐步写成的朴素模拟器（naiveSim），
// 以及用它与 Controller 做随机序列对照的测试。
// 模拟器刻意写得直白：每一步都按规则顺序显式展开，并输出判定依据。

import (
	"fmt"
	"math/rand"
	"testing"
)

// naiveSim 是朴素模拟器：与 Controller 无关的独立实现。
type naiveSim struct {
	cfg     Config
	cap     int64
	batches []Batch
	b0      int64
	lastOut int64
	lastIn  int64
	maxNow  int64
	evalNow int64
	seen    map[int64]bool
}

func newNaiveSim(cfg Config) *naiveSim {
	return &naiveSim{
		cfg:     cfg,
		cap:     cfg.Initial,
		lastOut: -1,
		lastIn:  -1,
		evalNow: -1,
		seen:    map[int64]bool{},
	}
}

func (s *naiveSim) inflightTotal() int64 {
	total := int64(0)
	for _, b := range s.batches {
		total += b.Count
	}
	return total
}

func (s *naiveSim) noneResult() Result {
	return Result{Action: ActionNone, Amount: 0, Cap: s.cap, Inflight: s.inflightTotal()}
}

// pickTier 朴素选档：lo<=v<下一档 lo，最后一档无上界。
func pickTier(tiers []Tier, v int64) int64 {
	pct := tiers[0].Pct
	for _, t := range tiers {
		if t.Lo <= v {
			pct = t.Pct
		} else {
			break
		}
	}
	return pct
}

// evaluate 返回结果与判定依据；被拒绝时结果为零值。
func (s *naiveSim) evaluate(now, metric int64) (Result, string, error) {
	// 1. 参数检查。
	if now < 0 || now > 1_000_000_000_000_000 || metric < 0 || metric > 1_000_000_000 {
		return Result{}, fmt.Sprintf("拒绝: 参数越界 (now=%d, metric=%d)", now, metric), ErrInvalidParam
	}
	// 2. 时钟检查。
	if now < s.maxNow {
		return Result{}, fmt.Sprintf("拒绝: 时钟回退 now=%d < maxNow=%d", now, s.maxNow), ErrClockBackward
	}
	// 3. 并入就绪批次（恰等即就绪）。
	merged := int64(0)
	var kept []Batch
	for _, b := range s.batches {
		if b.ReadyAt <= now {
			merged += b.Count
		} else {
			kept = append(kept, b)
		}
	}
	s.batches = kept
	s.cap += merged
	s.maxNow = now
	// 4. 同一 now 的重复 metric 去重。
	if now != s.evalNow {
		s.evalNow = now
		s.seen = map[int64]bool{}
	}
	if s.seen[metric] {
		return s.noneResult(), "去重: 同一 metric 在同一 now 已评估过，无动作", nil
	}
	s.seen[metric] = true
	// 5. 判定。
	return s.decide(now, metric, merged)
}

func (s *naiveSim) decide(now, metric, merged int64) (Result, string, error) {
	prefix := fmt.Sprintf("并入=%d后 cap=%d 在途=%d", merged, s.cap, s.inflightTotal())
	eff := s.cap + s.inflightTotal()

	// 扩容方向：metric >= H。
	if metric >= s.cfg.High {
		v := metric - s.cfg.High
		pct := pickTier(s.cfg.Up, v)
		inCooldown := s.lastOut >= 0 && now < s.lastOut+s.cfg.CoolOut
		base := eff
		baseName := "eff"
		if inCooldown {
			base = s.b0
			baseName = "B0"
		}
		delta := (base*pct + 99) / 100
		if delta < s.cfg.MinStep {
			delta = s.cfg.MinStep
		}
		target := base + delta
		if target > s.cfg.Max {
			target = s.cfg.Max
		}
		reason := fmt.Sprintf("%s; 扩容: v=%d pct=%d 冷却内=%v base=%s=%d delta=%d target=%d eff=%d",
			prefix, v, pct, inCooldown, baseName, base, delta, target, eff)
		if target > eff {
			add := target - eff
			s.batches = append(s.batches, Batch{ReadyAt: now + s.cfg.Warmup, Count: add})
			if !inCooldown {
				s.b0 = eff
				s.lastOut = now
			}
			reason += fmt.Sprintf("; 追加批次(%d,%d)", now+s.cfg.Warmup, add)
			return Result{Action: ActionScaleOut, Amount: add, Cap: s.cap, Inflight: s.inflightTotal()}, reason, nil
		}
		reason += "; target<=eff，无动作"
		return s.noneResult(), reason, nil
	}

	// 缩容方向：metric < Lw。
	if metric < s.cfg.Low {
		u := s.cfg.Low - metric
		pct := pickTier(s.cfg.Down, u)
		reason := fmt.Sprintf("%s; 缩容: u=%d pct=%d", prefix, u, pct)
		if s.inflightTotal() > 0 {
			return s.noneResult(), reason + "; 存在在途批次，阻止", nil
		}
		if s.lastIn >= 0 && now < s.lastIn+s.cfg.CoolIn {
			return s.noneResult(), reason + fmt.Sprintf("; 缩容冷却内 (now=%d < %d)，无动作", now, s.lastIn+s.cfg.CoolIn), nil
		}
		delta := s.cap * pct / 100
		if delta < 1 {
			delta = 1
		}
		target := s.cap - delta
		if target < s.cfg.Min {
			target = s.cfg.Min
		}
		reason += fmt.Sprintf("; delta=%d target=%d cap=%d", delta, target, s.cap)
		if target < s.cap {
			amount := s.cap - target
			s.cap = target
			s.lastIn = now
			return Result{Action: ActionScaleIn, Amount: amount, Cap: s.cap, Inflight: 0}, reason + "; 执行缩容", nil
		}
		return s.noneResult(), reason + "; target==cap，无动作", nil
	}

	// 死区。
	return s.noneResult(), prefix + "; 死区 [Lw,H)，无动作", nil
}

// randTiers 生成合法随机档表。
func randTiers(r *rand.Rand) []Tier {
	n := 1 + r.Intn(4)
	tiers := make([]Tier, 0, n)
	lo := int64(0)
	for i := 0; i < n; i++ {
		tiers = append(tiers, Tier{Lo: lo, Pct: 1 + r.Int63n(300)})
		lo += 1 + r.Int63n(15)
	}
	return tiers
}

// randConfig 生成合法随机配置（含 Cout/Cin 可能为 0）。
func randConfig(r *rand.Rand) Config {
	mn := int64(1) + r.Int63n(20)
	mx := mn + r.Int63n(60)
	cfg := Config{
		Min:     mn,
		Max:     mx,
		Initial: mn + r.Int63n(mx-mn+1),
		High:    1 + r.Int63n(200),
		Up:      randTiers(r),
		Down:    randTiers(r),
		MinStep: 1 + r.Int63n(5),
		Warmup:  1 + r.Int63n(10),
		CoolOut: r.Int63n(16),
		CoolIn:  r.Int63n(16),
	}
	cfg.Low = r.Int63n(cfg.High)
	return cfg
}

type evalStep struct {
	now    int64
	metric int64
}

// randSequence 生成随机评估序列：含同一 now 重复、死区、边界、
// 少量非法参数与时钟回退。
func randSequence(r *rand.Rand, cfg Config, length int) []evalStep {
	steps := make([]evalStep, 0, length)
	now := int64(0)
	for i := 0; i < length; i++ {
		switch x := r.Intn(100); {
		case x < 3: // 非法 now
			steps = append(steps, evalStep{now: -1 - r.Int63n(5), metric: r.Int63n(cfg.High + 50)})
			continue
		case x < 5: // 非法 metric
			steps = append(steps, evalStep{now: now, metric: 1_000_000_001 + r.Int63n(100)})
			continue
		case x < 8: // 时钟回退
			if now > 0 {
				steps = append(steps, evalStep{now: now - 1 - r.Int63n(now), metric: r.Int63n(cfg.High + 50)})
				continue
			}
		case x < 30: // 同一 now（触发去重/冷却边界）
		default:
			now += r.Int63n(8)
		}
		var metric int64
		switch r.Intn(10) {
		case 0:
			metric = cfg.High // 恰等 H
		case 1:
			metric = cfg.Low // 恰等 Lw
		case 2:
			metric = cfg.High + cfg.Up[r.Intn(len(cfg.Up))].Lo // 恰等扩容档边界
		case 3:
			lo := cfg.Down[r.Intn(len(cfg.Down))].Lo
			if cfg.Low >= lo {
				metric = cfg.Low - lo // 恰等缩容档边界
			} else {
				metric = r.Int63n(cfg.High + 50)
			}
		default:
			metric = r.Int63n(cfg.High + 50)
		}
		steps = append(steps, evalStep{now: now, metric: metric})
	}
	return steps
}

// TestRandomSequencesAgainstNaiveSim 用 2000 组随机评估序列
// 对照 Controller 与朴素模拟器，并校验不变量与重放确定性。
func TestRandomSequencesAgainstNaiveSim(t *testing.T) {
	r := rand.New(rand.NewSource(20261003))
	const groups = 2000
	for g := 0; g < groups; g++ {
		cfg := randConfig(r)
		steps := randSequence(r, cfg, 25)

		ctrl, err := New(cfg)
		if err != nil {
			t.Fatalf("group %d: New() error = %v", g, err)
		}
		replay, err := New(cfg)
		if err != nil {
			t.Fatalf("group %d: New() for replay error = %v", g, err)
		}
		sim := newNaiveSim(cfg)

		t.Logf("group %d: cfg=%+v", g, cfg)
		for i, st := range steps {
			got, gerr := ctrl.Evaluate(st.now, st.metric)
			want, reason, werr := sim.evaluate(st.now, st.metric)

			// 重放确定性：同样的序列在全新控制器上得到同样的结果。
			got2, gerr2 := replay.Evaluate(st.now, st.metric)
			if got2 != got || gerr2 != gerr {
				t.Fatalf("group %d step %d: 重放不一致 (%+v,%v) vs (%+v,%v)",
					g, i, got2, gerr2, got, gerr)
			}

			t.Logf("group %d step %d: Evaluate(%d, %d) -> got=(%v,%d,cap=%d,inflight=%d) err=%v | 依据: %s",
				g, i, st.now, st.metric, got.Action, got.Amount, got.Cap, got.Inflight, gerr, reason)

			if (gerr == nil) != (werr == nil) {
				t.Fatalf("group %d step %d: 错误不一致 got=%v want=%v", g, i, gerr, werr)
			}
			if gerr != nil {
				continue // 被拒绝：不产生结果，状态不变
			}
			if got != want {
				t.Fatalf("group %d step %d: 结果不一致 got=%+v want=%+v (依据: %s)",
					g, i, got, want, reason)
			}
			// 不变量：Mn<=cap<=Mx，cap+在途<=Mx，批次就绪时刻>maxNow。
			s := ctrl.State()
			if s.Cap < cfg.Min || s.Cap > cfg.Max {
				t.Fatalf("group %d step %d: cap=%d 越界 [%d,%d]", g, i, s.Cap, cfg.Min, cfg.Max)
			}
			if s.Effective > cfg.Max {
				t.Fatalf("group %d step %d: eff=%d > Mx=%d", g, i, s.Effective, cfg.Max)
			}
			for _, b := range s.Batches {
				if b.ReadyAt <= s.MaxNow {
					t.Fatalf("group %d step %d: 批次 %+v 就绪时刻 <= maxNow=%d", g, i, b, s.MaxNow)
				}
			}
			// 内部状态一致性：cap、批次、B0、lastOut、lastIn、maxNow 与模拟器相同。
			if s.Cap != sim.cap || s.B0 != sim.b0 || s.LastOut != sim.lastOut ||
				s.LastIn != sim.lastIn || s.MaxNow != sim.maxNow ||
				!batchesEqual(s.Batches, sim.batches) {
				t.Fatalf("group %d step %d: 状态不一致 ctrl=%+v sim=%+v", g, i, s, sim)
			}
		}
	}
}
