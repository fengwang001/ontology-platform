package rollout

import (
	"math/rand"
	"testing"
)

// naiveBatch 是朴素模拟器里按 readyAt 记录的一个就绪批次。
type naiveBatch struct {
	readyAt int64
	count   int64
}

// naivePlanner 严格按题目文字逐步写成，不做任何优化。
type naivePlanner struct {
	n, s, u, mr, p int64
	maxNow         int64
	a, b, c, d     int64
	batches        []naiveBatch
	stall          int64
}

func newNaive(N int64, PS, PU int, MR int64, P int) *naivePlanner {
	s := (N*int64(PS) + 99) / 100
	u := N * int64(PU) / 100
	if s == 0 && u == 0 {
		u = 1
	}
	return &naivePlanner{n: N, s: s, u: u, mr: MR, p: int64(P), a: N}
}

func (q *naivePlanner) cs(now int64) int64 {
	var sum int64
	for _, bt := range q.batches {
		if now-bt.readyAt >= q.mr {
			sum += bt.count
		}
	}
	return sum
}

func (q *naivePlanner) done(now int64) bool {
	return q.a+q.b == 0 && q.c == q.n && q.cs(now) == q.n
}

func iMin(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

func iMax(x, y int64) int64 {
	if x > y {
		return x
	}
	return y
}

func (q *naivePlanner) step(now int64) (StepResult, error) {
	if now < 0 {
		return StepResult{}, ErrInvalidArg
	}
	if now < q.maxNow {
		return StepResult{}, ErrClockRewind
	}
	startTotal := q.a + q.b + q.c + q.d
	startAvail := q.a + q.cs(now)

	up := iMax(0, iMin(q.n+q.s-startTotal, q.n-(q.c+q.d)))
	r1 := q.b
	r2 := iMin(q.a, iMax(0, startAvail-(q.n-q.u)))

	q.d += up
	q.b -= r1
	q.a -= r2
	q.maxNow = now

	if q.done(now) {
		q.stall = 0
	} else if up+r1+r2 > 0 {
		q.stall = 0
	} else {
		q.stall++
	}
	return StepResult{up, r1, r2, q.stall >= q.p}, nil
}

func (q *naivePlanner) newReady(k, now int64) error {
	if k < 1 || now < 0 {
		return ErrInvalidArg
	}
	if now < q.maxNow {
		return ErrClockRewind
	}
	if k > q.d {
		return ErrNoUnreadyNew
	}
	q.d -= k
	q.c += k
	merged := false
	for i := range q.batches {
		if q.batches[i].readyAt == now {
			q.batches[i].count += k
			merged = true
			break
		}
	}
	if !merged {
		q.batches = append(q.batches, naiveBatch{readyAt: now, count: k})
	}
	q.maxNow = now
	q.stall = 0
	return nil
}

func (q *naivePlanner) newFail(k, now int64) error {
	if k < 1 || now < 0 {
		return ErrInvalidArg
	}
	if now < q.maxNow {
		return ErrClockRewind
	}
	if k > q.c {
		return ErrNoReadyNew
	}
	remaining := k
	// 朴素实现：每次重新选 readyAt 最大的非空批扣除。
	for remaining > 0 {
		idx := -1
		for i := range q.batches {
			if q.batches[i].count > 0 && (idx == -1 || q.batches[i].readyAt > q.batches[idx].readyAt) {
				idx = i
			}
		}
		take := iMin(q.batches[idx].count, remaining)
		q.batches[idx].count -= take
		remaining -= take
	}
	compact := q.batches[:0]
	for _, bt := range q.batches {
		if bt.count > 0 {
			compact = append(compact, bt)
		}
	}
	q.batches = compact
	q.c -= k
	q.d += k
	q.maxNow = now
	return nil
}

func (q *naivePlanner) oldUnready(k, now int64) error {
	if k < 1 || now < 0 {
		return ErrInvalidArg
	}
	if now < q.maxNow {
		return ErrClockRewind
	}
	if k > q.a {
		return ErrNoReadyOld
	}
	q.a -= k
	q.b += k
	q.maxNow = now
	return nil
}

type event struct {
	op     string // step, ready, fail, old
	k, now int64
}

// 对 2000 组随机事件序列，朴素模拟与 Planner 逐步对照：
// 返回错误、Step 结果、各类实例数、批次、停滞计数、maxNow，并校验全部不变量。
func TestRandomAgainstNaive2000(t *testing.T) {
	const seeds = 2000
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		N := int64(1 + rng.Intn(20))
		PS := rng.Intn(101)
		PU := rng.Intn(101)
		MR := int64(rng.Intn(5)) // 小 MR 更易产生稳定/未稳定边界
		P := 1 + rng.Intn(5)

		real := mustNew(t, N, PS, PU, MR, P)
		naive := newNaive(N, PS, PU, MR, P)
		var events []event
		var now int64

		compare := func(t *testing.T, label string, gotErr, wantErr error, got StepResult, want StepResult) {
			t.Helper()
			if (gotErr == nil) != (wantErr == nil) ||
				(gotErr != nil && gotErr.Error() != wantErr.Error()) {
				t.Fatalf("seed=%d %s 错误不一致: got=%v want=%v\nevents=%v",
					seed, label, gotErr, wantErr, events)
			}
			if gotErr == nil && got != want {
				t.Fatalf("seed=%d %s 结果不一致: got=%+v want=%+v\nevents=%v",
					seed, label, got, want, events)
			}
			if real.a != naive.a || real.b != naive.b || real.c != naive.c || real.d != naive.d ||
				real.stall != naive.stall || real.maxNow != naive.maxNow {
				t.Fatalf("seed=%d %s 计数不一致: real(a=%d b=%d c=%d d=%d stall=%d now=%d) naive(a=%d b=%d c=%d d=%d stall=%d now=%d)\nevents=%v",
					seed, label, real.a, real.b, real.c, real.d, real.stall, real.maxNow,
					naive.a, naive.b, naive.c, naive.d, naive.stall, naive.maxNow, events)
			}
			if len(real.batches) != len(naive.batches) {
				t.Fatalf("seed=%d %s 批次数不一致: %v vs %v\nevents=%v",
					seed, label, real.batches, naive.batches, events)
			}
			for i := range real.batches {
				if real.batches[i] != (batch)(naive.batches[i]) {
					t.Fatalf("seed=%d %s 批次[%d]不一致: %v vs %v\nevents=%v",
						seed, label, i, real.batches, naive.batches, events)
				}
			}
			var bc int64
			for _, bt := range real.batches {
				bc += bt.count
			}
			if bc != real.c {
				t.Fatalf("seed=%d 批次之和=%d != c=%d\nevents=%v", seed, bc, real.c, events)
			}
		}

		Nevents := 30 + rng.Intn(70)
		for i := 0; i < Nevents; i++ {
			// now 单调不减；小概率重复同一 now、小概率故意回退以触发拒绝。
			if rng.Intn(8) == 0 {
				// 保持 now 不变
			} else {
				now += int64(rng.Intn(3))
			}
			reqNow := now
			if rng.Intn(10) == 0 && now > 0 {
				reqNow = now - 1 - int64(rng.Intn(3))
			}
			// 0/1/2=Step（权重高），3=NewReady，4=NewFail，5=OldUnready。
			opKind := rng.Intn(6)
			var k int64
			if opKind == 3 {
				k = int64(rng.Intn(int(real.d) + 3)) // 含 0 与超范围
			} else if opKind == 4 {
				k = int64(rng.Intn(int(real.c) + 3))
			} else if opKind == 5 {
				k = int64(rng.Intn(int(real.a) + 3))
			}
			ev := event{k: k, now: reqNow}

			var gotR, wantR StepResult
			var gotErr, wantErr error
			preAvail := real.a + real.stableLocked(real.maxNow)
			switch {
			case opKind <= 2:
				ev.op = "step"
				events = append(events, ev)
				gotR, gotErr = real.Step(reqNow)
				wantR, wantErr = naive.step(reqNow)
				compare(t, "Step", gotErr, wantErr, gotR, wantR)
				if gotErr == nil {
					total := real.a + real.b + real.c + real.d
					if total > real.n+real.s {
						t.Fatalf("seed=%d total=%d > N+S=%d\nevents=%v", seed, total, real.n+real.s, events)
					}
					if real.c+real.d > real.n {
						t.Fatalf("seed=%d c+d=%d > N=%d\nevents=%v", seed, real.c+real.d, real.n, events)
					}
					postAvail := real.a + real.stableLocked(reqNow)
					floor := preAvail
					if real.n-real.u < floor {
						floor = real.n - real.u
					}
					if postAvail < floor {
						t.Fatalf("seed=%d 可用数 %d < min(前=%d,N-U=%d)\nevents=%v",
							seed, postAvail, preAvail, real.n-real.u, events)
					}
				}
			case opKind == 3:
				ev.op = "ready"
				events = append(events, ev)
				gotErr = real.NewReady(k, reqNow)
				wantErr = naive.newReady(k, reqNow)
				compare(t, "NewReady", gotErr, wantErr, gotR, wantR)
			case opKind == 4:
				ev.op = "fail"
				events = append(events, ev)
				gotErr = real.NewFail(k, reqNow)
				wantErr = naive.newFail(k, reqNow)
				compare(t, "NewFail", gotErr, wantErr, gotR, wantR)
			default:
				ev.op = "old"
				events = append(events, ev)
				gotErr = real.OldUnready(k, reqNow)
				wantErr = naive.oldUnready(k, reqNow)
				compare(t, "OldUnready", gotErr, wantErr, gotR, wantR)
			}
		}
		// Done 以最近接受操作的 now 判定，两实现应一致。
		if real.Done() != naive.done(naive.maxNow) {
			t.Fatalf("seed=%d Done 不一致: real=%v naive=%v\nevents=%v",
				seed, real.Done(), naive.done(naive.maxNow), events)
		}
		if seed < 3 || testing.Verbose() {
			t.Logf("seed=%d 输入 N=%d PS=%d PU=%d MR=%d P=%d => S=%d U=%d；%d 个事件重放一致，最终 a=%d b=%d c=%d d=%d stall=%d done=%v",
				seed, N, PS, PU, MR, P, real.s, real.u, len(events),
				real.a, real.b, real.c, real.d, real.stall, real.Done())
		}
	}
}
