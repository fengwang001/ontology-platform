package api_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/txn"
)

// TestFacadeRules 仅经公开接口表驱动核验 Apply/Commit 各分支与位点推进。
func TestFacadeRules(t *testing.T) {
	t.Parallel()
	type act struct {
		op    byte
		seq   int64
		eff   int64
		wantE error
		wantC int64
	}
	cases := []struct {
		name string
		acts []act
	}{
		{"happy path", []act{{'a', 1, 10, nil, 0}, {'c', 1, 0, nil, 1},
			{'a', 2, 20, nil, 1}, {'c', 2, 0, nil, 2}}},
		{"idempotent after commit", []act{{'a', 1, 10, nil, 0}, {'c', 1, 0, nil, 1},
			{'a', 1, 99, nil, 1}, {'c', 1, 0, nil, 1}}},
		{"reject out of order", []act{{'a', 2, 2, txn.ErrOutOfOrder, 0}}},
		{"reject missing effect", []act{{'c', 1, 0, txn.ErrEffectMissing, 0}}},
		{"reject offset jump", []act{{'c', 3, 0, txn.ErrOffsetJump, 0}}},
		{"reject invalid seq", []act{{'a', 0, 1, txn.ErrInvalidSeq, 0},
			{'c', -7, 0, txn.ErrInvalidSeq, 0}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := api.New()
			for i, a := range tc.acts {
				var err error
				if a.op == 'a' {
					err = x.Apply(a.seq, a.eff)
				} else {
					err = x.Commit(a.seq)
				}
				if !errors.Is(err, a.wantE) || x.Committed() != a.wantC {
					t.Fatalf("act %d: err=%v C=%d want err=%v C=%d", i, err, x.Committed(), a.wantE, a.wantC)
				}
			}
		})
	}
}

// TestFacadeSelfCheck：对外自检必须通过四条不变量与 O(1) 核验。
func TestFacadeSelfCheck(t *testing.T) {
	t.Parallel()
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestFacadeRejectedThenUsable：拒绝不留痕，门面随后仍可正常完成提交。
func TestFacadeRejectedThenUsable(t *testing.T) {
	t.Parallel()
	x := api.New()
	for i, e := range []error{x.Apply(0, 1), x.Apply(5, 5), x.Commit(2), x.Commit(1)} {
		if e == nil {
			t.Fatalf("bad op %d succeeded", i)
		}
	}
	if x.Committed() != 0 || len(x.Pending()) != 0 {
		t.Fatalf("left state C=%d pending=%v", x.Committed(), x.Pending())
	}
	if x.Apply(1, 10) != nil || x.Commit(1) != nil || x.Committed() != 1 {
		t.Fatal("unusable after reject")
	}
}

// TestFacadeRestartReprocess：崩在两阶段之间，Restart 后经 Pending 重投并提交。
func TestFacadeRestartReprocess(t *testing.T) {
	t.Parallel()
	x := api.New()
	if x.Apply(1, 10) != nil || x.Commit(1) != nil || x.Apply(2, 20) != nil || x.Restart() != nil {
		t.Fatal("setup")
	}
	p := x.Pending()
	if x.Committed() != 1 || len(p) != 1 || p[0] != 2 {
		t.Fatalf("after restart C=%d pending=%v", x.Committed(), p)
	}
	if x.Apply(p[0], 20) != nil || x.Commit(2) != nil || x.Committed() != 2 {
		t.Fatal("reprocess failed")
	}
}

// TestFacadeConcurrentDuplicateApply：并发重复 Apply 同一在途序号后一次 Commit，
// C 恰好 +1、Pending 清空；并发读 Committed 单调不减。无 sleep。
func TestFacadeConcurrentDuplicateApply(t *testing.T) {
	t.Parallel()
	x := api.New()
	if x.Apply(1, 1) != nil || x.Commit(1) != nil {
		t.Fatal("setup")
	}
	const N, R = 32, 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < R; j++ {
				_ = x.Apply(2, 20)
			}
		}()
	}
	stop := make(chan struct{})
	var regress atomic.Bool
	var rd sync.WaitGroup
	rd.Add(1)
	go func() {
		defer rd.Done()
		prev := x.Committed()
		for {
			select {
			case <-stop:
				return
			default:
				if cur := x.Committed(); cur < prev {
					regress.Store(true)
				} else {
					prev = cur
				}
			}
		}
	}()
	close(start)
	wg.Wait()
	if x.Commit(2) != nil || x.Committed() != 2 || len(x.Pending()) != 0 {
		t.Fatalf("C=%d pending=%v", x.Committed(), x.Pending())
	}
	close(stop)
	rd.Wait()
	if regress.Load() {
		t.Fatal("Committed observed decreasing")
	}
}
