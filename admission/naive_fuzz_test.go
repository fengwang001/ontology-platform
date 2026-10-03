package admission

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveJob / naiveSim 是按题面规则独立写成的逐步（逐单位运行）朴素模拟，
// 不参考 Controller 的任何内部实现，用于对拍。
type naiveJob struct {
	id      string
	remain  int64
	C       int64
	d       int64
	M       int64
	v       int64
	started bool
}

type naiveSim struct {
	now     int64
	value   int64
	jobs    []*naiveJob // 始终保持 EDF 序：(d,id) 升序
	evicted []string
}

type naiveState struct {
	now     int64
	value   int64
	jobs    []*naiveJob
	evicted []string
}

func (s *naiveSim) cloneState() naiveState {
	jobs := make([]*naiveJob, len(s.jobs))
	for i, j := range s.jobs {
		cp := *j
		jobs[i] = &cp
	}
	return naiveState{s.now, s.value, jobs, append([]string(nil), s.evicted...)}
}

func (s *naiveSim) restoreState(x naiveState) {
	jobs := make([]*naiveJob, len(x.jobs))
	for i, j := range x.jobs {
		cp := *j
		jobs[i] = &cp
	}
	s.now = x.now
	s.value = x.value
	s.jobs = jobs
	s.evicted = x.evicted
}

func edfLess(a, b *naiveJob) bool {
	return a.d < b.d || (a.d == b.d && a.id < b.id)
}

// advance 逐单位运行：每个时钟单位执行 EDF 队首 1 个单位，
// 执行过即已开始；某单位跑完恰好完成则按完成时刻结算。
func (s *naiveSim) advance(to int64) {
	for s.now < to {
		s.now++
		if len(s.jobs) == 0 {
			continue
		}
		h := s.jobs[0]
		h.started = true
		h.remain--
		if h.remain == 0 {
			f := s.now
			late := f - h.d
			if late < 0 {
				late = 0
			}
			s.value += h.v * (h.M + 1 - late) / (h.M + 1)
			s.jobs = s.jobs[1:]
		}
	}
}

func (s *naiveSim) feasible() bool {
	var t int64 = s.now
	ok := true
	for _, j := range s.jobs {
		t += j.remain
		if t-j.d > j.M {
			ok = false
		}
	}
	return ok
}

func densityLess(a, b *naiveJob) bool {
	lhs := a.v * b.remain
	rhs := b.v * a.remain
	if lhs != rhs {
		return lhs < rhs
	}
	return a.id > b.id
}

func (s *naiveSim) submit(id string, now, C, d, M, v int64) (bool, RejectReason, []string) {
	saved := s.cloneState()
	s.advance(now)
	for _, j := range s.jobs {
		if j.id == id {
			s.restoreState(saved)
			return false, RejectDuplicateID, nil
		}
	}
	if now+C > d+M {
		s.restoreState(saved)
		return false, RejectInfeasible, nil
	}
	nj := &naiveJob{id: id, remain: C, C: C, d: d, M: M, v: v}
	s.jobs = append(s.jobs, nj)
	sort.SliceStable(s.jobs, func(i, k int) bool { return edfLess(s.jobs[i], s.jobs[k]) })
	if s.feasible() {
		return true, Accepted, nil
	}
	var kicked []string
	for {
		var victim *naiveJob
		for _, j := range s.jobs {
			if j.started {
				continue
			}
			if victim == nil || densityLess(j, victim) {
				victim = j
			}
		}
		if victim == nil {
			s.restoreState(saved)
			return false, RejectOverload, nil
		}
		idx := -1
		for i, j := range s.jobs {
			if j == victim {
				idx = i
				break
			}
		}
		s.jobs = append(s.jobs[:idx], s.jobs[idx+1:]...)
		if victim == nj {
			s.restoreState(saved)
			return false, RejectOverload, nil
		}
		kicked = append(kicked, victim.id)
		if s.feasible() {
			s.evicted = append(s.evicted, kicked...)
			return true, Accepted, kicked
		}
	}
}

func validArgs(id string, now, C, d, M, v int64) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	return now >= 0 && now <= 1e15 && C >= 1 && C <= 1e6 &&
		d >= 0 && d <= 1e15 && M >= 0 && M <= 1e6 && v >= 1 && v <= 1e6
}

type op struct {
	kind            string
	id              string
	now, C, d, M, v int64
}

func genOps(rng *rand.Rand) []op {
	var ops []op
	n := 1 + rng.Intn(60)
	var clock int64
	used := map[string]bool{}
	seq := 0
	newID := func() string {
		id := fmt.Sprintf("j%03d", seq)
		seq++
		used[id] = true
		return id
	}
	for i := 0; i < n; i++ {
		// 时间单调不减：多数小幅推进，偶尔跳跃或停在同一时刻。
		switch rng.Intn(10) {
		case 0, 1, 2:
			clock += int64(rng.Intn(6))
		case 3:
			clock += int64(rng.Intn(50))
		default:
		}
		if rng.Intn(6) == 0 {
			to := clock
			if rng.Intn(8) == 0 && to > 0 {
				to-- // 故意制造时钟回退
			}
			ops = append(ops, op{kind: "advance", now: to})
			if to >= clock {
				clock = to
			}
			continue
		}
		var id string
		if len(used) > 0 && rng.Intn(3) == 0 {
			ids := make([]string, 0, len(used))
			for k := range used {
				ids = append(ids, k)
			}
			id = ids[rng.Intn(len(ids))] // 可能与待处理作业重复（应被拒）
			// 从生成器的“占用”集合移除：下次若控制器中已完成则可成功复用。
			delete(used, id)
		} else {
			id = newID()
		}
		C := int64(1 + rng.Intn(12))
		d := clock + int64(rng.Intn(30))
		M := int64(rng.Intn(4))
		v := int64(1 + rng.Intn(20))
		o := op{kind: "submit", id: id, now: clock, C: C, d: d, M: M, v: v}
		// 约 12% 注入非法参数。
		if rng.Intn(8) == 0 {
			switch rng.Intn(6) {
			case 0:
				o.id = ""
			case 1:
				o.id = strings.Repeat("x", 33)
			case 2:
				o.C = 0
			case 3:
				o.C = 1_000_001
			case 4:
				o.M = -1
			case 5:
				o.v = 0
			}
		}
		if o.id == "" {
			delete(used, id)
		}
		ops = append(ops, o)
	}
	return ops
}

func refPending(c *Controller) []PendingItem { return c.Pending() }

func TestRandomDifferential2000(t *testing.T) {
	if !testing.Verbose() {
		t.Log("提示：加 -v 可查看每组序列的输入、输出与判定依据日志")
	}
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genOps(rng)
		ctrl := NewController()
		nav := &naiveSim{}
		var logb strings.Builder
		fmt.Fprintf(&logb, "seed=%d ops=%d\n", seed, len(ops))
		for step, o := range ops {
			switch o.kind {
			case "advance":
				rr := ctrl.Advance(o.now)
				// 朴素模型对非法/回退的处理与控制器对齐。
				var navOK bool
				var navReason RejectReason
				if o.now < 0 || o.now > 1e15 {
					navOK, navReason = false, RejectInvalidArgs
				} else if o.now < nav.now {
					navOK, navReason = false, RejectClockBack
				} else {
					nav.advance(o.now)
					navOK, navReason = true, Accepted
				}
				fmt.Fprintf(&logb, "  [%d] Advance(now=%d) -> ok=%v reason=%d | naive ok=%v reason=%d\n",
					step, o.now, rr.OK, rr.Reason, navOK, navReason)
				if rr.OK != navOK || rr.Reason != navReason {
					t.Fatalf("seed=%d Advance 不一致\n%s", seed, logb.String())
				}
			case "submit":
				argsValid := validArgs(o.id, o.now, o.C, o.d, o.M, o.v)
				rr := ctrl.Submit(o.id, o.now, o.C, o.d, o.M, o.v)
				var nOK bool
				var nReason RejectReason
				var nEv []string
				if !argsValid {
					nReason = RejectInvalidArgs
				} else if o.now < nav.now {
					nReason = RejectClockBack
				} else {
					nOK, nReason, nEv = nav.submit(o.id, o.now, o.C, o.d, o.M, o.v)
				}
				fmt.Fprintf(&logb, "  [%d] Submit(id=%q now=%d C=%d d=%d M=%d v=%d) -> accepted=%v reason=%d evicted=%v | naive accepted=%v reason=%d evicted=%v\n",
					step, o.id, o.now, o.C, o.d, o.M, o.v, rr.Accepted, rr.Reason, rr.Evicted, nOK, nReason, nEv)
				if rr.Accepted != nOK || rr.Reason != nReason || fmt.Sprint(rr.Evicted) != fmt.Sprint(nEv) {
					t.Fatalf("seed=%d Submit 不一致\n%s", seed, logb.String())
				}
			}
			// 每步后比对全部可观测状态。
			cp := refPending(ctrl)
			var np []PendingItem
			for _, j := range nav.jobs {
				np = append(np, PendingItem{ID: j.id, Remain: j.remain, Started: j.started})
			}
			if fmt.Sprint(cp) != fmt.Sprint(np) {
				t.Fatalf("seed=%d step=%d Pending 不一致 ctrl=%v naive=%v\n%s", seed, step, cp, np, logb.String())
			}
			if ctrl.Value() != nav.value {
				t.Fatalf("seed=%d step=%d Value 不一致 ctrl=%d naive=%d\n%s", seed, step, ctrl.Value(), nav.value, logb.String())
			}
			if fmt.Sprint(ctrl.Evicted()) != fmt.Sprint(nav.evicted) {
				t.Fatalf("seed=%d step=%d Evicted 不一致\n%s", seed, step, logb.String())
			}
			if ctrl.Now() != nav.now {
				t.Fatalf("seed=%d step=%d Now 不一致 %d!=%d\n%s", seed, step, ctrl.Now(), nav.now, logb.String())
			}
		}
		if testing.Verbose() && (seed < 5 || seed%500 == 0) {
			t.Logf("%s  -> final now=%d value=%d pending=%d evicted=%v",
				strings.TrimRight(logb.String(), "\n"), ctrl.Now(), ctrl.Value(), len(ctrl.Pending()), ctrl.Evicted())
		}
	}
}
