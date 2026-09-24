package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentPropose：N 个 goroutine 并发 Propose 互异 payload，结束后
// seq 必须构成 1..N 连续双射；随后 N 次 Deliver 严格按 1..N 顺序投出。
func TestConcurrentPropose(t *testing.T) {
	for _, N := range []int{1, 2, 8, 64, 256} {
		s := New()
		seqs := make([]int, N)
		var wg sync.WaitGroup
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				v, err := s.Propose(fmt.Sprintf("m%d", i))
				if err != nil {
					t.Errorf("propose %d: %v", i, err)
					return
				}
				seqs[i] = v
			}(i)
		}
		wg.Wait()

		seen := make([]bool, N+1) // seq -> 是否出现，双射即每个 1..N 恰一次
		bySeq := make(map[int]string, N)
		for i, v := range seqs {
			if v < 1 || v > N || seen[v] {
				t.Fatalf("N=%d seq set not bijection: i=%d v=%d", N, i, v)
			}
			seen[v] = true
			bySeq[v] = fmt.Sprintf("m%d", i)
		}
		for k := 1; k <= N; k++ {
			q, p, ok := s.Deliver()
			if !ok || q != k || p != bySeq[k] {
				t.Fatalf("N=%d delivery %d got (%d,%s,%v)", N, k, q, p, ok)
			}
		}
		if s.Delivered() != N {
			t.Fatalf("N=%d Delivered=%d", N, s.Delivered())
		}
		if _, _, ok := s.Deliver(); ok { // 抽干后不得多投
			t.Fatalf("N=%d extra delivery", N)
		}
	}
}

// TestConcurrentDeliveredAndSelfCheck：只读方法被多 goroutine 并发调用，race 干净。
func TestConcurrentDeliveredAndSelfCheck(t *testing.T) {
	s := New()
	for i := 0; i < 50; i++ {
		s.Propose("x")
	}
	const G, K = 16, 200
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < K; k++ {
				_ = s.Delivered()
				if err := s.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
					return
				}
				if g%4 == 0 {
					s.Deliver() // 与只读方法并发推进游标
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestSentinelErrors：api 透传四类互不相同错误，拒绝不留痕、之后仍可用。
func TestSentinelErrors(t *testing.T) {
	want := []error{ErrEmptyPayload, ErrAlreadyDelivered, ErrSeqOutOfRange, ErrSlotFilled}
	for i := 1; i < len(want); i++ {
		for j := 0; j < i; j++ {
			if errors.Is(want[i], want[j]) {
				t.Fatalf("sentinel %d,%d not distinct", i, j)
			}
		}
	}
	s := New()
	for _, p := range []string{"A", "B", "C", "D", "E"} {
		if _, err := s.Propose(p); err != nil {
			t.Fatal(err)
		}
	}
	s.Deliver()
	s.Deliver()
	s.Crash() // d=2, nextSeq=6, 空洞 3,4,5

	calls := []struct {
		call func() error
		want error
	}{
		{func() error { _, e := s.Propose(""); return e }, ErrEmptyPayload},
		{func() error { return s.RePropose(2, "x") }, ErrAlreadyDelivered},
		{func() error { return s.RePropose(6, "x") }, ErrSeqOutOfRange},
		{func() error {
			if e := s.RePropose(3, "C"); e != nil {
				return e
			}
			return s.RePropose(3, "X")
		}, ErrSlotFilled},
	}
	d0 := s.Delivered()
	for i, c := range calls {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("case %d err=%v want %v", i, err, c.want)
		}
		if s.Delivered() != d0 {
			t.Fatalf("case %d changed Delivered", i)
		}
	}
	s.RePropose(4, "D")
	s.RePropose(5, "E")
	got := []string{}
	for i := 0; i < 3; i++ {
		_, p, ok := s.Deliver()
		if !ok {
			t.Fatalf("delivery %d missing after refill", i)
		}
		got = append(got, p)
	}
	if got[0] != "C" || got[1] != "D" || got[2] != "E" || s.Delivered() != 5 {
		t.Fatalf("after refill got=%v d=%d", got, s.Delivered())
	}
}

// TestSelfCheck：对外自检必须通过。
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
