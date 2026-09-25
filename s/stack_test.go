package s

import (
	"math/rand"
	"sync"
	"testing"
)

func chk(t *testing.T, ok bool, f string, a ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(f, a...)
	}
}
func mustNew(n int) *Stack { st, _ := New(n); return st }
func TestEightSteps(t *testing.T) {
	st, _ := New(3)
	wantLen := []int{1, 2, 3, 3, 2, 1, 0, 0}
	for step := 1; step <= 8; step++ {
		if step <= 4 {
			err := st.Push(step)
			chk(t, step != 4 || err == ErrFull, "step4 err=%v", err)
		} else {
			v, ok := st.Pop()
			wv, wok := 8-step, step != 8 // 步5/6/7 -> 3/2/1,true；步8 -> 0,false
			chk(t, v == wv && ok == wok, "step%d (%d,%v) want (%d,%v)", step, v, ok, wv, wok)
		}
		chk(t, st.Len() == wantLen[step-1], "step%d Len=%d", step, st.Len())
	}
}

func TestLIFOConservation(t *testing.T) {
	for _, n := range []int{1, 2, 17, 500} {
		st, _ := New(n)
		for i := 0; i < n; i++ {
			err := st.Push(i)
			chk(t, err == nil && st.Len() == i+1, "n=%d push %d", n, i)
		}
		for i := n - 1; i >= 0; i-- {
			v, ok := st.Pop()
			chk(t, ok && v == i && st.Len() == i, "n=%d (%d,%v) Len=%d want %d", n, v, ok, st.Len(), i)
		}
	}
}

func TestEquivNaive(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 99, 2026} {
		st, ref, rng := mustNew(8), &refStack{}, rand.New(rand.NewSource(seed))
		for i := 0; i < 3000; i++ {
			if rng.Intn(10) < 6 {
				v := rng.Intn(100000)
				if err := st.Push(v); err == ErrFull {
					chk(t, ref.len() == 8, "seed %d: ref not full", seed)
				} else if err != nil {
					t.Fatal(err)
				} else {
					ref.push(v)
				}
			} else {
				v, ok := st.Pop()
				rv, rok := ref.pop()
				chk(t, v == rv && ok == rok && st.Len() == ref.len(), "seed %d (%d,%v)/%d vs ref (%d,%v)/%d", seed, v, ok, st.Len(), rv, rok, ref.len())
			}
		}
	}
}

func TestRejectedNoTrace(t *testing.T) {
	for _, bad := range []int{0, -1, -99} {
		_, err := New(bad)
		chk(t, err == ErrInvalidMaxLen, "New(%d)=%v", bad, err)
	}
	chk(t, ErrInvalidMaxLen != ErrFull && ErrFull != ErrClosed && ErrInvalidMaxLen != ErrClosed, "sentinels must differ")
	st := mustNew(2)
	_ = st.Push(10)
	_ = st.Push(20)
	err := st.Push(30)
	chk(t, err == ErrFull && st.Len() == 2, "full err=%v Len=%d", err, st.Len())
	v, ok := st.Pop()
	chk(t, ok && v == 20, "trace after full: (%d,%v)", v, ok)
	err = st.Close()
	chk(t, err == nil && st.Close() == ErrClosed, "repeat Close undecidable")
	err = st.Push(40)
	chk(t, err == ErrClosed && st.Len() == 1, "closed err=%v Len=%d", err, st.Len())
}

func TestCloseDrain(t *testing.T) {
	st := mustNew(3)
	for _, v := range []int{1, 2, 3} {
		_ = st.Push(v)
	}
	_ = st.Close()
	for _, want := range []int{3, 2, 1} {
		v, ok := st.Pop()
		chk(t, ok && v == want, "drain (%d,%v) want %d", v, ok, want)
	}
	_, ok := st.Pop()
	chk(t, !ok && st.Len() == 0, "drained closed stack must stay empty")
}

func TestPopVisitsOne(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		st := mustNew(m)
		for i := 0; i < m; i++ {
			_ = st.Push(i)
		}
		st.lastPopVisited.Store(0)
		v, ok := st.Pop()
		chk(t, ok && v == m-1 && st.lastPopVisited.Load() == 1, "m=%d (%d,%v) visited=%d", m, v, ok, st.lastPopVisited.Load())
	}
}

func TestConcurrentN(t *testing.T) {
	for _, N := range []int{1, 8, 64, 512} {
		st, start, out := mustNew(N), make(chan struct{}), make(chan int, N)
		var wg sync.WaitGroup
		wg.Add(2 * N)
		for i := 0; i < N; i++ {
			i := i
			go func() {
				defer wg.Done()
				<-start
				_ = st.Push(i) // 容量恰为 N，N 个 Push 不可能 ErrFull
			}()
			go func() {
				defer wg.Done()
				<-start
				for {
					if v, ok := st.Pop(); ok {
						out <- v
						return
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		close(out)
		seen := map[int]int{}
		for v := range out {
			seen[v]++
		}
		chk(t, len(seen) == N && st.Len() == 0, "N=%d distinct=%d Len=%d", N, len(seen), st.Len())
		for v, c := range seen {
			chk(t, c == 1, "N=%d value %d x%d", N, v, c)
		}
	}
}
