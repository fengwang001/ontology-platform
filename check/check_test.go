package check_test

import (
	"errors"
	"math/rand"
	"ontology/check"
	"ontology/mlfq"
	"testing"
)

var base = mlfq.Config{Levels: 3, Boost: 50, MaxJobs: 20000, Quotas: []int{4, 8, 16}}

func TestMatchesNaive(t *testing.T) { // 2.2 与朴素参照逐步一致
	s, n := mlfq.New(base), check.NewNaive(base.Quotas, base.Boost)
	r, nsub := rand.New(rand.NewSource(42)), 0
	for step := 0; step < 500; step++ {
		switch r.Intn(4) {
		case 0:
			s.Submit(1000+nsub, 1+nsub%20)
			n.Submit(1000+nsub, 1+nsub%20)
			nsub++
		case 1:
			id := 1000 + r.Intn(max(nsub, 1))
			s.Yield(id)
			n.Yield(id)
		default:
			gid, gok := s.Step()
			if nid, nok := n.Step(); gid != nid || gok != nok {
				t.Fatalf("step %d: got (%d,%v) want (%d,%v)", step, gid, gok, nid, nok)
			}
		}
	}
}
func TestGaming(t *testing.T) { // 第三节：配额本级累计，Yield 不清零
	s := mlfq.New(mlfq.Config{Levels: 2, Boost: 1000, MaxJobs: 10, Quotas: []int{4, 100}})
	s.Submit(1, 100) // 博弈：每跑 3 刻 Yield；2 号为老实作业
	s.Submit(2, 100)
	for step, w := range []int{1, 1, 1, 2, 2, 2, 2, 1, 2, 2, 2, 2} { // 第 8 步博弈作业降级
		if id, _ := s.Step(); id != w {
			t.Fatalf("step %d: ran %d, want %d", step, id, w)
		}
		if step == 2 {
			s.Yield(1)
		}
	}
	for i := 0; i < 20; i++ { // 对照：每次调度重置配额的错误实现
		if used := 3; used >= 4 { // 重置后跑 3 刻 Yield，永不降级
			t.Fatal("reset-per-schedule impl demoted the gamer")
		}
	}
}
func TestBoostBound(t *testing.T) { // 2.3 等待上界 B = S + n·Q[0]
	s := mlfq.New(base)
	for i := 0; i < 5; i++ {
		s.Submit(i, 100)
	}
	bound, last := base.Boost+5*base.Quotas[0], [5]int{}
	for step := 1; s.Stats().Active > 0; step++ {
		id, _ := s.Step()
		if step-last[id] > bound {
			t.Fatalf("job %d waited %d > bound %d", id, step-last[id], bound)
		}
		last[id] = step
	}
}
func TestErrors(t *testing.T) { // 2.5 哨兵错误且零副作用
	s := mlfq.New(mlfq.Config{Levels: 2, Boost: 10, MaxJobs: 1, Quotas: []int{2, 2}})
	s.Submit(1, 5)
	for got, want := range map[error]error{s.Submit(1, 9): mlfq.ErrDuplicate, s.Submit(2, 9): mlfq.ErrFull, s.Yield(99): mlfq.ErrUnknown} {
		if !errors.Is(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	}
	if s.Stats() != (mlfq.Stats{Active: 1}) {
		t.Error("error paths must be side-effect free")
	}
}
func TestConcurrentConservation(t *testing.T) { // 2.4 守恒 + 第五节并发
	s := mlfq.New(base)
	for i := 0; i < 2000; i++ {
		go s.Submit(i, 5)
	}
	ran, total := make([]int, 2000), 0
	for total < 2000*5 { // 总和固定为 2000*5：无人超过 5 刻即人人恰好 5 刻
		if id, ok := s.Step(); ok {
			ran[id]++
			total++
			if ran[id] > 5 {
				t.Fatalf("job %d ran %d ticks, want 5", id, ran[id])
			}
		}
	}
	for i := 0; i < 10000; i++ { // 第四节：单次 Step 检查数 ≤ L+1
		s.Submit(i, 1)
	}
	s.Step()
	if got := s.Stats().LastCheck; got > base.Levels+1 {
		t.Fatalf("checked %d queues > L+1=%d", got, base.Levels+1)
	}
}
