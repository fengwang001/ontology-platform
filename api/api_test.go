package api_test

import (
	"errors"
	"math"
	"sync"
	"testing"

	"ontology/api"
)

// TestApplyNetChangelog 表驱动核验对外接口的序列、净值与三类错误。
func TestApplyNetChangelog(t *testing.T) {
	steps := []struct {
		op    api.Op
		log   []int64
		net   int64
		errIs error
	}{
		{5, []int64{5}, 5, nil},
		{-5, []int64{}, 0, nil},
		{-8, []int64{-8}, -8, nil},
		{8, []int64{}, 0, nil},
		{7, []int64{7}, 7, nil},
		{2, []int64{7, 2}, 9, nil},
		{-7, []int64{7, 2, -7}, 2, nil}, // 末尾是 +2，不抵消
		{-2, []int64{7, 2, -7, -2}, 0, nil},
	}
	c := api.New(8)
	for i, s := range steps {
		err := c.Apply(s.op)
		if !errors.Is(err, s.errIs) {
			t.Fatalf("step %d err=%v", i+1, err)
		}
		if err != nil {
			continue
		}
		if got := c.Changelog(); len(got) != len(s.log) {
			t.Fatalf("step %d log=%v want=%v", i+1, got, s.log)
		} else {
			for j := range got {
				if got[j] != s.log[j] {
					t.Fatalf("step %d log=%v want=%v", i+1, got, s.log)
				}
			}
		}
		if c.Net() != s.net {
			t.Fatalf("step %d net=%d want=%d", i+1, c.Net(), s.net)
		}
	}

	// 非法增量：在八步实例上 +0 被拒（非法判定先于深度判定）。
	if err := c.Apply(0); !errors.Is(err, api.ErrInvalidIncrement) {
		t.Fatalf("zero err=%v", err)
	}
	// 深度超限：容量 1 的实例已有 1 条，再追加即被拒。
	d := api.New(1)
	if err := d.Apply(1); err != nil {
		t.Fatal(err)
	}
	if err := d.Apply(1); !errors.Is(err, api.ErrDepthExceeded) {
		t.Fatalf("depth err=%v", err)
	}
	// 净值溢出。
	o := api.New(8)
	if err := o.Apply(api.Op(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if err := o.Apply(1); !errors.Is(err, api.ErrNetOverflow) {
		t.Fatalf("overflow err=%v", err)
	}
}

// TestChangelogCopy 保证返回的是副本，调用方无法污染内部状态。
func TestChangelogCopy(t *testing.T) {
	c := api.New(4)
	c.Apply(1)
	c.Apply(2)
	got := c.Changelog()
	got[0] = 999
	again := c.Changelog()
	if again[0] != 1 {
		t.Fatalf("internal state mutated through returned slice: %v", again)
	}
}

// TestSelfCheck 对外自检必须通过内置序列的四条不变量核验。
func TestSelfCheck(t *testing.T) {
	if err := api.New(16).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentReadOnly N 个 goroutine 并发只读同一已喂满实例，
// 各自拿到的净值与未了结序列必须逐条相同。用起跑栅栏替代 sleep。
func TestConcurrentReadOnly(t *testing.T) {
	const N = 64
	c := api.New(4096)
	for i := int64(1); i <= 2000; i++ {
		if err := c.Apply(api.Op(i)); err != nil { // 互不抵消的正增量
			t.Fatal(err)
		}
	}
	wantNet := c.Net()
	wantLog := c.Changelog()

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 100; k++ {
				if c.Net() != wantNet {
					errs <- errors.New("net mismatch under concurrent reads")
					return
				}
				got := c.Changelog()
				if len(got) != len(wantLog) {
					errs <- errors.New("changelog length mismatch")
					return
				}
				for i := range got {
					if got[i] != wantLog[i] {
						errs <- errors.New("changelog entry mismatch")
						return
					}
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
