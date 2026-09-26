package api_test

import (
	"errors"
	"testing"

	"ontology/api"
	"ontology/vc"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// 第三节七步序列：每步之后的三个节点向量时钟与投递结果。
func TestSevenSteps(t *testing.T) {
	s := api.New(3)
	var m1, m2, m3 api.Msg
	ops := []func() bool{
		func() bool { m1, _ = s.Broadcast(0); return true },
		func() bool { d, _ := s.Deliver(1, m1); return d },
		func() bool { m2, _ = s.Broadcast(1); return true },
		func() bool { d, _ := s.Deliver(2, m1); return d },
		func() bool { d, _ := s.Deliver(2, m2); return d },
		func() bool { m3, _ = s.Broadcast(1); return true },
		func() bool { d, _ := s.Deliver(0, m3); return d },
	}
	want := [7][3][3]int{
		{{1, 0, 0}, {0, 0, 0}, {0, 0, 0}}, {{1, 0, 0}, {1, 0, 0}, {0, 0, 0}},
		{{1, 0, 0}, {1, 1, 0}, {0, 0, 0}}, {{1, 0, 0}, {1, 1, 0}, {1, 0, 0}},
		{{1, 0, 0}, {1, 1, 0}, {1, 1, 0}}, {{1, 0, 0}, {1, 2, 0}, {1, 1, 0}},
		{{1, 0, 0}, {1, 2, 0}, {1, 1, 0}},
	}
	wantD := [7]bool{true, true, true, true, true, true, false}
	for i, op := range ops {
		if d := op(); d != wantD[i] {
			t.Errorf("S%d: d=%v want %v", i+1, d, wantD[i])
		}
		for p := 0; p < 3; p++ {
			v, _ := s.VC(p)
			if !vc.Vector(v).Equal(want[i][p][:]) {
				t.Errorf("S%d: VC[%d]=%v want %v", i+1, p, v, want[i][p])
			}
		}
	}
}

// 不变量 1：任意操作序列后 VC 与朴素重算（按发送者计数已投递消息）一致。
func TestInvariantNaiveRecompute(t *testing.T) {
	for _, n := range []int{2, 3, 5} {
		for seed := uint32(1); seed <= 20; seed++ {
			s, r := api.New(n), seed
			next := func(m int) int { r = r*1664525 + 1013904223; return int(r>>16) % m }
			count := make([][]int, n)
			for i := range count {
				count[i] = make([]int, n)
			}
			var msgs []api.Msg
			for i := 0; i < 40; i++ {
				if next(2) == 0 || len(msgs) == 0 {
					m, err := s.Broadcast(next(n))
					must(t, err)
					msgs = append(msgs, m)
					count[m.From][m.From]++ // 自己广播视作已投递
					continue
				}
				m, p := msgs[next(len(msgs))], next(n)
				d, err := s.Deliver(p, m)
				if m.From == p {
					if !errors.Is(err, api.ErrSelfDelivery) {
						t.Fatalf("自投递应报 ErrSelfDelivery, got %v", err)
					}
					continue
				}
				must(t, err)
				if d {
					count[p][m.From]++
				}
			}
			for p := 0; p < n; p++ {
				v, _ := s.VC(p)
				if !vc.Vector(v).Equal(count[p]) {
					t.Fatalf("n=%d seed=%d: VC[%d]=%v 朴素重算=%v", n, seed, p, v, count[p])
				}
			}
		}
	}
}

// 并发：N 个 goroutine 并发只读同一节点 VC，逐字段相同；不用 sleep。
func TestConcurrentVCRead(t *testing.T) {
	s := api.New(3)
	m, _ := s.Broadcast(0)
	_, _ = s.Deliver(1, m)
	base, _ := s.VC(0)
	const G = 64
	start := make(chan struct{})
	res := make(chan []int, G)
	for g := 0; g < G; g++ {
		go func() {
			<-start
			v, err := s.VC(0)
			if err != nil {
				v = nil
			}
			res <- v
		}()
	}
	close(start)
	for i := 0; i < G; i++ {
		if v := <-res; !vc.Vector(base).Equal(v) {
			t.Fatalf("并发读到 %v, want %v", v, base)
		}
	}
}

// SelfCheck 内置序列核验四条不变量。
func TestSelfCheck(t *testing.T) {
	if err := api.New(3).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
