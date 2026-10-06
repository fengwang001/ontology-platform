package floorcontrol_test

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	fc "ontology/floorcontrol"
)

type callReq struct {
	op      Op
	gotErr  error
	gotPos  int
	gotSnap stateView
	done    chan struct{}
}

// TestConcurrentLinearizable：多个 goroutine 并发向同一个串行器提交调用。
// 串行器随机挑选一个待处理调用、为其分配单调 now 并同时驱动真实服务与朴素
// 模型，两者结果必须一致。提交是并发的，而执行顺序是某个合法串行顺序——
// 直接证明“并发调用等价于某个串行顺序，且该顺序可复现”。
func TestConcurrentLinearizable(t *testing.T) {
	const callers = 8
	const perCaller = 250

	for trial := 0; trial < 5; trial++ {
		room, _ := fc.New(7, 20)
		nav := newNaive(7, 20)
		rng := rand.New(rand.NewPCG(uint64(trial+7), 42))

		mustOK(t, room.Join("h", 0))
		nav.join("h", 0)
		users := []string{"h", "u0", "u1", "u2", "u3", "u4"}
		for i := 1; i < len(users); i++ {
			mustOK(t, room.Join(users[i], 0))
			nav.join(users[i], 0)
		}

		reqs := make(chan *callReq, callers*perCaller)
		kinds := []string{"Raise", "Lower", "Grant", "Yield", "Mute", "Unmute", "Appoint", "Dismiss", "QueuePos", "Snapshot"}

		var now int64
		var done sync.WaitGroup
		done.Add(1)
		go func() {
			defer done.Done()
			pending := make([]*callReq, 0)
			finished := 0
			total := callers * perCaller
			for finished < total {
				// 收集一批并发到达的请求。
				select {
				case r := <-reqs:
					pending = append(pending, r)
				default:
				}
				for len(pending) < 4 {
					select {
					case r := <-reqs:
						pending = append(pending, r)
						continue
					default:
					}
					break
				}
				if len(pending) == 0 {
					r := <-reqs
					pending = append(pending, r)
				}
				// 随机选一个请求作为下一条被线性化的调用。
				idx := rng.IntN(len(pending))
				r := pending[idx]
				pending = append(pending[:idx], pending[idx+1:]...)
				now += int64(1 + rng.IntN(4))
				if r.op.Kind != "Snapshot" {
					r.op.Now = now
				} else {
					r.op.Now = now
				}
				ro := runReal(room, r.op)
				no := runNaive(nav, r.op)
				if reason, equal := outcomesEqual(ro, no); !equal {
					t.Errorf("trial %d %s: %s", trial, r.op, reason)
					close(r.done)
					finished++
					continue
				}
				r.gotErr = ro.err
				r.gotPos = ro.pos
				r.gotSnap = ro.snap
				close(r.done)
				finished++
			}
		}()

		var submitted sync.WaitGroup
		for c := 0; c < callers; c++ {
			submitted.Add(1)
			go func(id int) {
				defer submitted.Done()
				crng := rand.New(rand.NewPCG(uint64(trial*1000+id+1), uint64(id+1)))
				for range perCaller {
					kind := kinds[crng.IntN(len(kinds))]
					u := users[crng.IntN(len(users))]
					r := &callReq{op: Op{Kind: kind, A: u}, done: make(chan struct{})}
					switch kind {
					case "Mute", "Unmute", "Appoint", "Dismiss":
						r.op.B = users[crng.IntN(len(users))]
					case "Snapshot":
						r.op.A = ""
					}
					reqs <- r
					<-r.done // 等待被线性化
				}
			}(c)
		}
		submitted.Wait()
		done.Wait()

		final := snap(t, room, now)
		ns, err := nav.snapshot(now)
		mustOK(t, err)
		if oKindView(snapshotOf(final)) != oKindView(naiveView(ns)) {
			t.Fatalf("trial %d final state mismatch\nreal: %+v\nnaive:%+v", trial, final, ns)
		}
	}
}

// TestConcurrentStressRace 在 -race 下以多 goroutine 直接并发调用真实服务，
// 原子计数器保证进入服务的 now 单调；验证无数据竞争且不变量始终成立。
func TestConcurrentStressRace(t *testing.T) {
	room, _ := fc.New(5, 50)
	mustOK(t, room.Join("h", 0))
	users := []string{"h", "a", "b", "c", "d", "e"}
	for i := 1; i < len(users); i++ {
		mustOK(t, room.Join(users[i], 0))
	}
	var clock atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for range 300 {
				now := clock.Add(1)
				u := users[int(now)%len(users)]
				tgt := users[(id+1)%len(users)]
				switch now % 8 {
				case 0:
					_ = room.Raise(u, now)
				case 1:
					_ = room.Lower(u, now)
				case 2:
					_ = room.Grant("h", now)
				case 3:
					_ = room.Yield(u, now)
				case 4:
					_ = room.Mute("h", tgt, now)
				case 5:
					_ = room.Unmute("h", tgt, now)
				case 6:
					_, _ = room.QueuePos(u, now)
				default:
					_, _ = room.Snapshot(now)
				}
			}
		}(w)
	}
	wg.Wait()
	s := snap(t, room, clock.Load()+100)
	seen := map[string]bool{}
	for _, q := range s.Queue {
		if seen[q] {
			t.Fatalf("duplicate queued member %s", q)
		}
		seen[q] = true
		if q == s.Speaker {
			t.Fatalf("speaker %s also queued", q)
		}
	}
}
