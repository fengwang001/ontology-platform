package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/rule"
)

func genOps(r *rand.Rand, n int) []func(*System) error {
	var ops []func(*System) error
	for len(ops) < n {
		if r.Intn(3) == 0 {
			ops = append(ops, pub(rule.Put(fmt.Sprintf("r%d", r.Intn(4)), int64(r.Intn(20)))))
		} else {
			ops = append(ops, snd(int64(r.Intn(8)), int64(r.Intn(25))))
		}
	}
	return ops
}

func drive(s *System, ops []func(*System) error, r *rand.Rand) {
	_, prev := s.Versions()
	for _, f := range ops {
		if err := f(s); err != nil {
			panic(err)
		}
		if r != nil && r.Intn(2) == 0 { // 随机穿插：把当前欠的版本全部投递
			if err := deliverAll(s); err != nil {
				panic(err)
			}
		}
		g, vs := s.Versions()
		for i, v := range vs {
			if v < prev[i] || v > g {
				panic("v_i 越界或回退")
			}
			prev[i] = v
			tags := s.BufTags(i)
			for j, x := range tags {
				if x <= v || (j > 0 && tags[j-1] > x) {
					panic("缓冲区违反不变量")
				}
			}
		}
	}
	if err := deliverAll(s); err != nil {
		panic(err)
	}
}

func TestNaiveConsistency(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		r := rand.New(rand.NewSource(seed))
		s := New(3, 1<<20)
		drive(s, genOps(r, 60), r)
		if !rule.SameHits(s.Output(), rule.Naive(s.log.Updates(), s.acc)) {
			t.Fatalf("seed=%d 与朴素参照不一致", seed)
		}
	}
}

func TestDeliveryTimingIndependence(t *testing.T) {
	for _, seed := range []int64{6, 7, 8} {
		r := rand.New(rand.NewSource(seed))
		ops := genOps(r, 60)
		a, b := New(3, 1<<20), New(3, 1<<20)
		drive(a, ops, r)   // 随机投递穿插
		drive(b, ops, nil) // 全部压到最后
		if !rule.SameHits(a.Output(), b.Output()) {
			t.Fatalf("seed=%d 命中集合随投递时机变化", seed)
		}
		oa, ob := a.Output(), b.Output()
		slices.SortStableFunc(oa, func(x, y rule.Hit) int { return x.Inst - y.Inst })
		slices.SortStableFunc(ob, func(x, y rule.Hit) int { return x.Inst - y.Inst })
		if !slices.Equal(oa, ob) { // 稳定排序后逐条相同 ⇔ 各实例内命中顺序相同
			t.Fatalf("seed=%d 实例内命中顺序不同", seed)
		}
	}
}

func TestVersionBufferInvariants(t *testing.T) {
	for _, seed := range []int64{9, 10, 11} {
		drive(New(3, 1<<20), genOps(rand.New(rand.NewSource(seed)), 60), rand.New(rand.NewSource(seed+100)))
	}
}

func TestRejectionNoTrace(t *testing.T) {
	s := New(2, 1)
	_ = s.Publish(rule.Put("r1", 10))
	_ = s.Send(0, 1) // 占满实例 0 缓冲
	before := fmt.Sprint(s.Versions()) + fmt.Sprint(s.Output(), s.BufTags(0))
	bads := []error{s.Publish(rule.Put("", 1)), s.Publish(rule.Delete("x")), s.Deliver(0, 2), s.Deliver(0, 0), s.Deliver(9, 1), s.Send(-1, 1), s.Send(2, 1)}
	kinds := []error{rule.ErrEmptyID, rule.ErrNoSuch, ErrDeliver, ErrDeliver, ErrDeliver, ErrBadKey, ErrBufFull}
	for i, got := range bads {
		for j, u := range []error{rule.ErrEmptyID, rule.ErrNoSuch, ErrDeliver, ErrBadKey, ErrBufFull} {
			if errors.Is(got, u) != errors.Is(kinds[i], u) { // 每个错误恰好匹配自己的类别
				t.Errorf("bads[%d]=%v 对第%d类判定错误", i, got, j)
			}
		}
	}
	if fmt.Sprint(s.Versions())+fmt.Sprint(s.Output(), s.BufTags(0)) != before || s.Deliver(0, 1) != nil {
		t.Fatal("被拒后状态改变或无法继续使用")
	}
}

func TestConcurrency(t *testing.T) {
	for _, tc := range [][2]int{{2, 100}, {4, 200}} {
		s := New(tc[0], 10000)
		var log []rule.Update
		for i := 0; i < 10; i++ {
			log = append(log, rule.Put(fmt.Sprintf("r%d", i), int64(i)))
			_ = s.Publish(log[i])
		}
		prev := make([]int, tc[0])
		var wg sync.WaitGroup
		for i := 0; i < tc[0]; i++ { // 每个实例一个 goroutine 并发投递到 G
			wg.Add(1)
			go func(i int) { defer wg.Done(); _ = s.Deliver(i, 10) }(i)
		}
		wg.Add(1)
		go func() { // 并发发送，期间读到的各 v_i 单调不减
			defer wg.Done()
			for i := 0; i < tc[1]; i++ {
				_ = s.Send(int64(i%tc[0]), int64(i%11))
				_, vs := s.Versions()
				for j, v := range vs {
					if v < prev[j] {
						t.Errorf("v_%d 回退", j)
					}
					prev[j] = v
				}
			}
		}()
		wg.Wait()
		if !rule.SameHits(s.Output(), rule.Naive(s.log.Updates(), s.acc)) {
			t.Fatal("并发结果与朴素参照不一致")
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if New(2, 8).SelfCheck() != nil {
		t.Fail()
	}
}
