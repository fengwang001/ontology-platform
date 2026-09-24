package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/broker"
)

// call 字段序：(pid, epoch, partition, seq)。
type call [4]int64

func send(a *api.API, c call) (int64, bool, error) {
	return a.Produce(c[0], c[1], int(c[2]), c[3], "")
}

// TestTenStepDerivation 钉住第三节 N=2,W=2 的十步推导：判定、位点与两个分区最终日志。
func TestTenStepDerivation(t *testing.T) {
	ten := []call{{7, 0, 0, 0}, {7, 0, 1, 0}, {7, 0, 0, 1}, {7, 0, 0, 1}, {7, 0, 0, 3},
		{7, 0, 0, 2}, {7, 0, 0, 0}, {7, 1, 1, 0}, {7, 0, 0, 3}, {7, 1, 0, 0}}
	offWant := []int64{0, 0, 1, 1, 0, 2, 0, 1, 0, 3}
	dupWant := []bool{false, false, false, true, false, false, false, false, false, false}
	errWant := []error{nil, nil, nil, nil, broker.ErrOutOfOrder, nil, broker.ErrDuplicateExpired, nil, broker.ErrFenced, nil}
	a := api.New(2, 2)
	for i, c := range ten {
		off, dup, err := send(a, c)
		if off != offWant[i] || dup != dupWant[i] || !errors.Is(err, errWant[i]) {
			t.Fatalf("step %d got (%d,%v,%v) want (%d,%v,%v)", i, off, dup, err, offWant[i], dupWant[i], errWant[i])
		}
	}
	if l0, l1 := a.Log(0), a.Log(1); len(l0) != 4 || len(l1) != 2 || l0[3].Epoch != 1 || l1[1].Epoch != 1 {
		t.Fatalf("final logs = %d,%d with wrong epoch tail (want 4,2)", len(l0), len(l1))
	}
}

// TestNaiveEquivalence 调内置自检；SelfCheck 内含朴素参照（升级逐个分区 delete、
// 窗口线性查找）对随机请求序列的逐条对拍，并覆盖其余三条不变量。
func TestNaiveEquivalence(t *testing.T) {
	for _, c := range [][2]int{{1, 1}, {2, 2}, {5, 3}} {
		if err := api.New(c[0], c[1]).SelfCheck(); err != nil {
			t.Fatalf("n=%d w=%d SelfCheck: %v", c[0], c[1], err)
		}
	}
}

// TestFence 钉住围栏：epoch 升级接受后，任何更小 epoch（含跨分区）都不落盘。
func TestFence(t *testing.T) {
	a := api.New(2, 3)
	a.Produce(1, 0, 0, 0, "")
	a.Produce(1, 2, 0, 0, "")
	for _, c := range []call{{1, 0, 0, 1}, {1, 1, 0, 0}, {1, 1, 1, 1}} {
		if off, dup, err := send(a, c); !errors.Is(err, broker.ErrFenced) || off != 0 || dup {
			t.Fatalf("%v got (%d,%v,%v) want fenced", c, off, dup, err)
		}
	}
	if l0, l1 := a.Log(0), a.Log(1); len(l0) != 2 || len(l1) != 0 {
		t.Fatalf("logs after fence = %d,%d want 2,0", len(l0), len(l1))
	}
}

// TestRejectLeavesNoTrace 钉住失败不留痕：拒绝前后日志不变；seq!=0 的升级不写
// epoch 表；被拒后仍可正常使用。哨兵互异由 broker 包 TestExactlyOnce 顺带钉住。
func TestRejectLeavesNoTrace(t *testing.T) {
	a := api.New(1, 2)
	for _, s := range []int64{0, 1, 2} { // W=2：窗口 [1,2]，seq0 已过期
		a.Produce(1, 0, 0, s, "")
	}
	cs := []call{{-1, 0, 0, 0}, {1, -1, 0, 0}, {1, 0, 0, -1}, {1, 0, 5, 0},
		{1, 0, 0, 0}, {1, 0, 0, 5}, {2, 5, 0, 3}}
	es := []error{broker.ErrInvalid, broker.ErrInvalid, broker.ErrInvalid, broker.ErrInvalid,
		broker.ErrDuplicateExpired, broker.ErrOutOfOrder, broker.ErrOutOfOrder}
	for i, c := range cs {
		before := len(a.Log(0))
		if _, _, err := send(a, c); !errors.Is(err, es[i]) || len(a.Log(0)) != before {
			t.Fatalf("%v got %v (want %v) or left a trace", c, err, es[i])
		}
	}
	a.Produce(1, 1, 0, 0, "")
	if _, _, err := a.Produce(1, 0, 0, 0, ""); !errors.Is(err, broker.ErrFenced) || len(a.Log(0)) != 4 {
		t.Fatal("fence case left a trace or misclassified")
	}
	if off, _, err := a.Produce(2, 0, 0, 0, ""); err != nil || off != 4 { // pid2 未被偷升级到 epoch5
		t.Fatalf("rejected bump upgraded epoch: off=%d err=%v", off, err)
	}
	if _, _, err := a.Produce(1, 1, 0, 1, ""); err != nil { // 被拒后仍可正常使用
		t.Fatalf("normal use after rejections: %v", err)
	}
}

// TestConcurrentDuplicateProduce：K 个 goroutine 各按 seq 升序重发同一组 0..S-1，
// 被拒即原样重试，无 sleep。最终日志恰好 S 条，同 seq 各路位点完全相同。
func TestConcurrentDuplicateProduce(t *testing.T) {
	const K, S = 8, 6
	a := api.New(1, S)
	var wg sync.WaitGroup
	offs := make([][S]int64, K)
	for g := 0; g < K; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for s := int64(0); s < S; s++ {
				for {
					if off, _, err := a.Produce(7, 0, 0, s, ""); err == nil {
						offs[g][s] = off
						break
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if l := a.Log(0); len(l) != S {
		t.Fatalf("log len=%d want %d", len(l), S)
	} else {
		for i, r := range l {
			if r.Seq != int64(i) {
				t.Fatalf("log[%d].Seq=%d want %d exactly once", i, r.Seq, i)
			}
		}
	}
	for s := 0; s < S; s++ {
		for g := 1; g < K; g++ {
			if offs[g][s] != offs[0][s] || offs[0][s] != int64(s) {
				t.Fatalf("seq=%d offsets %d vs %d (want %d)", s, offs[g][s], offs[0][s], s)
			}
		}
	}
}
