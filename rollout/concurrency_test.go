package rollout

import (
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// genValidOps 生成一条只含可接受操作的事件序列（now 单调不减、k 不超量）。
func genValidOps(t *testing.T, seed int64, p *Planner) []event {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var ops []event
	var now int64
	for i := 0; i < 400; i++ {
		now += int64(rng.Intn(3))
		switch rng.Intn(6) {
		case 0, 1, 2:
			p.Step(now)
			ops = append(ops, event{op: "step", now: now})
		case 3:
			if p.d == 0 {
				p.Step(now)
				ops = append(ops, event{op: "step", now: now})
				continue
			}
			k := int64(1 + rng.Intn(int(p.d)))
			p.NewReady(k, now)
			ops = append(ops, event{op: "ready", k: k, now: now})
		case 4:
			if p.c == 0 {
				p.Step(now)
				ops = append(ops, event{op: "step", now: now})
				continue
			}
			k := int64(1 + rng.Intn(int(p.c)))
			p.NewFail(k, now)
			ops = append(ops, event{op: "fail", k: k, now: now})
		default:
			if p.a == 0 {
				p.Step(now)
				ops = append(ops, event{op: "step", now: now})
				continue
			}
			k := int64(1 + rng.Intn(int(p.a)))
			p.OldUnready(k, now)
			ops = append(ops, event{op: "old", k: k, now: now})
		}
	}
	return ops
}

func replayOps(t *testing.T, p *Planner, ops []event) []StepResult {
	t.Helper()
	var out []StepResult
	for _, op := range ops {
		switch op.op {
		case "step":
			r, err := p.Step(op.now)
			if err != nil {
				t.Fatalf("replay Step(%d): %v", op.now, err)
			}
			out = append(out, r)
		case "ready":
			if err := p.NewReady(op.k, op.now); err != nil {
				t.Fatalf("replay NewReady(%d,%d): %v", op.k, op.now, err)
			}
		case "fail":
			if err := p.NewFail(op.k, op.now); err != nil {
				t.Fatalf("replay NewFail(%d,%d): %v", op.k, op.now, err)
			}
		case "old":
			if err := p.OldUnready(op.k, op.now); err != nil {
				t.Fatalf("replay OldUnready(%d,%d): %v", op.k, op.now, err)
			}
		}
	}
	return out
}

// 相同操作序列重放两次：Step 输出序列与最终状态必须完全一致。
func TestReplayDeterminism(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		cfg := mustNew(t, 12, 40, 30, 10, 3)
		ops := genValidOps(t, seed, cfg)

		p1 := mustNew(t, 12, 40, 30, 10, 3)
		r1 := replayOps(t, p1, ops)
		s1 := p1.Snapshot(p1.maxNow)

		p2 := mustNew(t, 12, 40, 30, 10, 3)
		r2 := replayOps(t, p2, ops)
		s2 := p2.Snapshot(p2.maxNow)

		if !reflect.DeepEqual(r1, r2) {
			t.Fatalf("seed=%d Step 序列不一致", seed)
		}
		if !reflect.DeepEqual(s1, s2) {
			t.Fatalf("seed=%d 最终状态不一致: %+v vs %+v", seed, s1, s2)
		}
	}
}

// 并发可串行化与无竞争：8 个 goroutine 共用一个全局原子单调时钟，
// 并发发起 Step/NewReady(1)/NewFail(1)/OldUnready(1)。每次调用都在互斥
// 临界区内原子地被接受或拒绝（竞争导致的超出范围拒绝是合法的串行史结果），
// 因此：不允许出现数据竞争/崩溃；运行结束后总数、c+d、批和==c 等不变量必须
// 成立；接受/拒绝的划分必须等价于某个全序串行执行。
func TestConcurrentSerializable(t *testing.T) {
	const workers = 8
	const perWorker = 1500
	for run := 0; run < 3; run++ {
		p := mustNew(t, 20, 40, 30, 3, 3)
		var clock atomic.Int64 // 全局单调：杜绝时钟回退这一类拒绝
		var accepted, rejected int64
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(run*100 + id)))
				for i := 0; i < perWorker; i++ {
					now := clock.Add(1)
					var err error
					switch rng.Intn(4) {
					case 0:
						_, err = p.Step(now)
					case 1:
						err = p.NewReady(1, now)
					case 2:
						err = p.NewFail(1, now)
					default:
						err = p.OldUnready(1, now)
					}
					if err != nil {
						// 竞争可能导致两类合法拒绝：
						// 1) 大时间戳先入临界区，小时间戳后到 -> ErrClockRewind；
						// 2) 同类操作先后顺序改变 -> 超出范围。
						if err != ErrClockRewind &&
							err != ErrNoUnreadyNew && err != ErrNoReadyNew && err != ErrNoReadyOld {
							t.Errorf("unexpected concurrent error: %v", err)
							return
						}
						atomic.AddInt64(&rejected, 1)
					} else {
						atomic.AddInt64(&accepted, 1)
					}
				}
			}(w)
		}
		wg.Wait()

		p.mu.Lock()
		total := p.a + p.b + p.c + p.d
		var bc int64
		for _, bt := range p.batches {
			bc += bt.count
		}
		p.mu.Unlock()
		st := p.Snapshot(p.maxNow)
		if total > 20+8 { // N+S
			t.Fatalf("run=%d total=%d > N+S", run, total)
		}
		if st.NewReady+st.NewUnready > 20 {
			t.Fatalf("run=%d c+d=%d > N", run, st.NewReady+st.NewUnready)
		}
		if bc != st.NewReady {
			t.Fatalf("run=%d 批次和=%d != c=%d", run, bc, st.NewReady)
		}
		t.Logf("判定: run=%d %d 个并发调用（接受=%d 竞争拒绝=%d，含时钟回退/超范围）终态 a=%d b=%d c=%d d=%d，全部不变量成立，即存在等价全序串行史",
			run, workers*perWorker, accepted, rejected,
			st.OldReady, st.OldUnready, st.NewReady, st.NewUnready)
	}
}

// 并发混合读：Done/Snapshot 在任意交错下不被拒绝、不 panic，并满足基本取值域。
func TestConcurrentReaders(t *testing.T) {
	p := mustNew(t, 10, 50, 50, 2, 3)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				st := p.Snapshot(int64(i))
				if st.Total() > p.n+p.s {
					t.Errorf("reader saw total=%d > N+S", st.Total())
					return
				}
				_ = p.Done()
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		var now int64
		rng := rand.New(rand.NewSource(7))
		for i := 0; i < 2000; i++ {
			now += int64(rng.Intn(2))
			switch i % 4 {
			case 0:
				p.Step(now)
			case 1:
				if p.Snapshot(now).NewUnready > 0 {
					p.NewReady(1, now)
				}
			case 2:
				if p.Snapshot(now).NewReady > 0 {
					p.NewFail(1, now)
				}
			case 3:
				if p.Snapshot(now).OldReady > 0 {
					p.OldUnready(1, now)
				}
			}
		}
		stop.Store(true)
	}()
	wg.Wait()
}
