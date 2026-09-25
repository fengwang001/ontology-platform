package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// Emitted 按构造单调（dispatch 顺序追加 1..next），故首=1 且末=长度即无空洞。
func prefixOK(em []int64) bool {
	return len(em) == 0 || em[0] == 1 && em[len(em)-1] == int64(len(em))
}
func expectDrain(t *testing.T, m int, in []int64, last, drop int64) {
	r, _ := New(m)
	for _, s := range in {
		r.Feed("K", s)
	}
	if em := r.Emitted("K"); int64(len(em)) != last || !prefixOK(em) || r.Buffered("K") != 0 || r.Dropped() != drop {
		t.Fatalf("in=%v em=%v d=%d", in, em, r.Dropped())
	}
}
func TestFeedScenarios(t *testing.T) {
	// [0]=到达seq [1]=期望本次发射 [2]=期望步后缓冲（maxInFlight=3）。
	tab := [][3][]int64{
		{{5}, nil, {5}},
		{{2}, nil, {2, 5}},
		{{4}, nil, {2, 4, 5}},
		{{1}, {1, 2}, {4, 5}}, // 第4步：发射1并级联放行2
		{{3}, {3, 4, 5}, nil},
		{{6}, {6}, nil},
		{{7}, {7}, nil},
		{{2}, nil, nil}, // 重复丢弃
	}
	r, _ := New(3)
	for i, x := range tab {
		got, err := r.Feed("K", x[0][0])
		bu := r.hub.BufferedSnapshot("K")
		if err != nil || !slices.Equal(got, x[1]) || !slices.Equal(bu, x[2]) {
			t.Fatalf("step %d: (%v,%v) bu=%v want %v/%v", i+1, got, err, bu, x[1], x[2])
		}
	}
	if r.Dropped() != 1 {
		t.Fatalf("dropped=%d", r.Dropped())
	}
	expectDrain(t, 2, []int64{1, 2}, 2, 0)
	expectDrain(t, 3, []int64{3, 2, 1}, 3, 0)
	expectDrain(t, 5, []int64{5, 4, 3, 2, 1}, 5, 0)
	expectDrain(t, 3, []int64{3, 3, 2, 1, 1}, 3, 1) // 在途重投幂等 + 发射后重投计丢弃
	expectDrain(t, 1, []int64{2, 1}, 2, 0)
}
func TestBackpressureBound(t *testing.T) {
	for _, m := range []int{1, 2, 3, 5} {
		r, _ := New(m)
		for s := int64(2); s <= int64(m)+1; s++ {
			r.Feed("K", s)
		}
		old := r.hub.BufferedSnapshot("K")
		_, e := r.Feed("K", int64(m)+2)
		if !errors.Is(e, ErrBackpressure) || r.Buffered("K") != m ||
			!slices.Equal(r.hub.BufferedSnapshot("K"), old) {
			t.Fatalf("m=%d bound/trace violated: %v", m, e)
		}
	}
}
func TestErrorsLeaveNoTrace(t *testing.T) {
	if ErrEmptyKey == ErrInvalidMax || ErrEmptyKey == ErrBackpressure || ErrInvalidMax == ErrBackpressure {
		t.Fatal("sentinels not distinct")
	}
	if _, e := New(0); !errors.Is(e, ErrInvalidMax) {
		t.Fatal(e)
	}
	r, _ := New(1)
	r.Feed("K", 3) // 此后状态必为 emitted=0/buffered=1/dropped=0
	if _, e := r.Feed("", 1); !errors.Is(e, ErrEmptyKey) {
		t.Fatal(e)
	}
	if _, e := r.Feed("K", 2); !errors.Is(e, ErrBackpressure) {
		t.Fatal(e)
	}
	got := fmt.Sprintf("%d/%d/%d", len(r.Emitted("K")), r.Buffered("K"), r.Dropped())
	if got != "0/1/0" || r.SelfCheck() != nil {
		t.Fatalf("trace=%q or unusable after reject", got)
	}
}
func TestOrderingMatchesNaive(t *testing.T) {
	streams := [][]int64{{5, 2, 4, 1, 3, 6, 7, 2}, {9, 8, 7, 6, 5, 4, 3, 2, 1, 1, 4}}
	for L := 1; L <= 30; L++ {
		s := make([]int64, L) // 逆序到达：循环生成，含拒绝与重复的极端乱序
		for i := range s {
			s[i] = int64(L - i)
		}
		streams = append(streams, s)
	}
	for _, m := range []int{1, 2, 3, 8} {
		for _, s := range streams {
			if e := checkNaive(m, s); e != nil {
				t.Fatalf("m=%d: %v", m, e)
			}
		}
	}
}
func TestSelfCheck(t *testing.T) {
	for _, m := range []int{1, 3, 10} {
		r, _ := New(m)
		if e := r.SelfCheck(); e != nil {
			t.Fatalf("m=%d: %v", m, e)
		}
	}
}
func TestConcurrentDistinctKeys(t *testing.T) {
	const N, L = 8, 300
	r, _ := New(L)
	start := make(chan struct{})
	var done atomic.Bool
	var rd, wr sync.WaitGroup
	rd.Add(1)
	go func() {
		defer rd.Done()
		<-start
		for !done.Load() {
			for g := 0; g < N; g++ { // 并发读到的每个 key 序列必须严格递增无空洞
				if !prefixOK(r.Emitted(fmt.Sprintf("g%d", g))) {
					t.Errorf("hole g%d", g)
				}
			}
			_ = r.Dropped()
		}
	}()
	for g := 0; g < N; g++ {
		wr.Add(1)
		go func(g int) {
			defer wr.Done()
			<-start
			for i := 0; i < L; i++ { // 确定性乱序，无 sleep；容量 L 不触发背压
				r.Feed(fmt.Sprintf("g%d", g), int64((i*7+3)%L)+1)
			}
			if em := r.Emitted(fmt.Sprintf("g%d", g)); len(em) != L || !prefixOK(em) {
				t.Errorf("g%d final %v", g, em)
			}
		}(g)
	}
	close(start)
	wr.Wait()
	done.Store(true)
	rd.Wait()
}
