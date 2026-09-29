package termination

import (
	"math/rand/v2"
	"testing"
)

// replayRun 是确定性模拟器的一次执行记录：相同种子重放必须得到完全相同的
// 操作序列、宣告时刻（步数）与宣告轮数。
type replayRun struct {
	steps    []string
	annStep  int
	annRound int
	maxRound int
}

// simulate 用固定种子的伪随机数驱动检测器：发送、投递、转空闲、令牌传递
// 以完全确定的顺序交错执行（具体选择仅依赖此前已发生的随机数）。
// 当系统进入静止（无在途消息且全员空闲）后停止发送，只做投递/空闲/令牌。
func simulate(t *testing.T, seed, n int64, maxSteps int) *replayRun {
	t.Helper()
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
	d, err := New(int(n))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	run := &replayRun{annStep: -1}
	quiet := false

	for step := 1; step <= maxSteps; step++ {
		if r, ok := d.Announced(); ok {
			run.annStep = step - 1
			run.annRound = r
			break
		}

		// 静止检测一旦发生就保持：之后不再注入新消息。
		if !quiet {
			snap := d.Snapshot()
			if snap.Pending == 0 && allIdle(snap.States) {
				quiet = true
			}
		}

		snap := d.Snapshot()
		actives := activeList(snap.States)
		move := chooseMove(rng, d, quiet, n)
		switch move {
		case 0: // 发送
			sendOne(t, d, rng, n, step, run)
		case 1: // 投递最早的在途消息
			msg := d.net.Pending()[0]
			if _, err := d.Deliver(msg.ID); err != nil {
				t.Fatalf("step %d Deliver(%d): %v", step, msg.ID, err)
			}
			run.steps = append(run.steps, "deliver")
		case 2: // 任一活跃进程转空闲
			p := actives[rng.IntN(len(actives))]
			if err := d.BecomeIdle(p); err != nil {
				t.Fatalf("step %d BecomeIdle(%d): %v", step, p, err)
			}
			run.steps = append(run.steps, "idle")
		case 3: // 空闲令牌持有者传递令牌
			h := snap.Holder
			ann, r, err := d.PassToken(h)
			if err != nil {
				t.Fatalf("step %d PassToken(%d): %v", step, h, err)
			}
			run.steps = append(run.steps, "pass")
			if r > run.maxRound {
				run.maxRound = r
			}
			if ann {
				run.annStep = step
				run.annRound = r
			}
		}

		// 每步后验证基本不变量：计数器之和 == 在途消息数。
		snap = d.Snapshot()
		if sum(snap.Counts) != snap.Pending {
			t.Fatalf("step %d invariant violated: sum(counts)=%d pending=%d",
				step, sum(snap.Counts), snap.Pending)
		}
		if snap.Pending < 0 {
			t.Fatalf("step %d negative pending", step)
		}
	}

	if run.annStep < 0 {
		t.Fatalf("seed %d: termination not announced within %d steps (quiet=%v)", seed, maxSteps, quiet)
	}

	// 宣告时：全员空闲、无在途消息、计数全 0。
	snap := d.Snapshot()
	if snap.Pending != 0 {
		t.Fatalf("seed %d: announced with %d pending messages", seed, snap.Pending)
	}
	if !allIdle(snap.States) {
		t.Fatalf("seed %d: announced while a process is active", seed)
	}
	if sum(snap.Counts) != 0 {
		t.Fatalf("seed %d: announced with nonzero count sum %d", seed, sum(snap.Counts))
	}
	return run
}

// feasibleMoves 在当前状态下列出一定能成功的动作：
// 0 发送（非静止且 n>1 且存在活跃进程）、1 投递、2 转空闲、3 令牌传递。
func feasibleMoves(d *Detector, quiet bool, n int64) []int {
	snap := d.Snapshot()
	var moves []int
	actives := activeList(snap.States)
	if !quiet && n > 1 && len(actives) > 0 {
		moves = append(moves, 0)
	}
	if snap.Pending > 0 {
		moves = append(moves, 1)
	}
	if len(actives) > 0 {
		moves = append(moves, 2)
	}
	if snap.States[snap.Holder] == Idle {
		moves = append(moves, 3)
	}
	return moves
}

func chooseMove(rng *rand.Rand, d *Detector, quiet bool, n int64) int {
	moves := feasibleMoves(d, quiet, n)
	if len(moves) == 0 {
		panic("no feasible move while not terminated")
	}
	return moves[rng.IntN(len(moves))]
}

func activeList(states []State) []int {
	var actives []int
	for p, st := range states {
		if st == Active {
			actives = append(actives, p)
		}
	}
	return actives
}

func sendOne(t *testing.T, d *Detector, rng *rand.Rand, n int64, step int, run *replayRun) {
	t.Helper()
	snap := d.Snapshot()
	actives := activeList(snap.States)
	from := actives[rng.IntN(len(actives))]
	to := rng.IntN(int(n))
	if to == from {
		to = (to + 1) % int(n)
	}
	if _, err := d.Send(from, to); err != nil {
		t.Fatalf("step %d Send(%d,%d): %v", step, from, to, err)
	}
	run.steps = append(run.steps, "send")
}

func allIdle(states []State) bool {
	for _, st := range states {
		if st != Idle {
			return false
		}
	}
	return true
}

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

func TestRandomInterleavings1000(t *testing.T) {
	const runs, maxSteps = 1000, 20000
	for seed := int64(1); seed < runs; seed++ {
		n := int64(3 + seed%6)
		first := simulate(t, seed, n, maxSteps)
		// 相同种子 + 相同进程数重放，必须复现完全相同的轨迹与宣告时刻、轮数。
		again := simulate(t, seed, n, maxSteps)
		if !equalRuns(first, again) {
			t.Fatalf("replay mismatch for seed %d:\nfirst: step=%d round=%d\nagain: step=%d round=%d",
				seed, first.annStep, first.annRound, again.annStep, again.annRound)
		}
	}
}

func equalRuns(a, b *replayRun) bool {
	if a.annStep != b.annStep || a.annRound != b.annRound || len(a.steps) != len(b.steps) {
		return false
	}
	for i := range a.steps {
		if a.steps[i] != b.steps[i] {
			return false
		}
	}
	return true
}
