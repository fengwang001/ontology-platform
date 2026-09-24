package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

// TestAPISelfCheck：对外 SelfCheck 必须通过内置八步序列、四类错误与 O(1) 多档核验。
func TestAPISelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestAPIEightSteps：只经公开接口重放第三节八步，核验存活数、发布值与第 8 步的 UAF。
func TestAPIEightSteps(t *testing.T) {
	s := api.New()
	if _, err := s.Acquire(); !errors.Is(err, api.ErrEmptyStore) {
		t.Fatalf("empty acquire: %v", err)
	}
	if err := s.Publish(10); err != nil {
		t.Fatal(err)
	}
	h1, err := s.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := s.Acquire()
	if s.AliveCount() != 1 {
		t.Fatalf("step3 alive=%d", s.AliveCount())
	}
	if err := s.Publish(20); err != nil || s.AliveCount() != 2 {
		t.Fatalf("step4: alive=%d err=%v", s.AliveCount(), err)
	}
	h3, _ := s.Acquire()
	for _, h := range []*api.Handle{h1, h2} {
		if v, err := h.Get(); err != nil || v != 10 { // 历史版本被句柄钉住
			t.Fatalf("shared get: %d %v", v, err)
		}
	}
	if err := h1.Release(); err != nil || s.AliveCount() != 2 {
		t.Fatalf("step6: alive=%d err=%v", s.AliveCount(), err)
	}
	if err := h2.Release(); err != nil || s.AliveCount() != 1 { // v1 立即回收
		t.Fatalf("step7: alive=%d err=%v", s.AliveCount(), err)
	}
	if _, err := h1.Get(); !errors.Is(err, api.ErrUseAfterFree) { // 第 8 步
		t.Fatalf("step8 get: %v", err)
	}
	if v, err := h3.Get(); err != nil || v != 20 {
		t.Fatalf("survivor: %d %v", v, err)
	}
}

// TestAPIRejectedOps：四类哨兵互不相同，经公开接口全部可判定；被拒后状态不变、仍可用。
func TestAPIRejectedOps(t *testing.T) {
	sentinels := []error{api.ErrDoubleRelease, api.ErrUseAfterFree, api.ErrEmptyStore, api.ErrNegativeValue}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if sentinels[i] == sentinels[j] {
				t.Fatalf("sentinels %d/%d equal", i, j)
			}
		}
	}
	s := api.New()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"empty-acquire", func() error { _, e := s.Acquire(); return e }, api.ErrEmptyStore},
		{"negative-publish", func() error { return s.Publish(-1) }, api.ErrNegativeValue},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	if s.AliveCount() != 0 {
		t.Fatalf("empty rejects left state: %d", s.AliveCount())
	}
	if err := s.Publish(7); err != nil { // 拒绝后首发正常
		t.Fatal(err)
	}
	h, _ := s.Acquire()
	before := s.AliveCount()
	if err := h.Release(); err != nil { // 正常释放
		t.Fatal(err)
	}
	if err := h.Release(); !errors.Is(err, api.ErrDoubleRelease) { // 双重释放
		t.Fatalf("double: %v", err)
	}
	if _, err := h.Get(); !errors.Is(err, api.ErrUseAfterFree) { // 已释放句柄 Get
		t.Fatalf("uaf: %v", err)
	}
	if err := s.Publish(-9); !errors.Is(err, api.ErrNegativeValue) || s.AliveCount() != before {
		t.Fatalf("negative after use changed state: alive=%d", s.AliveCount())
	}
}

// TestAPIConcurrent：N 个 goroutine 经公开接口 Acquire 后立即 Release，无 sleep、无负计数。
func TestAPIConcurrent(t *testing.T) {
	const N = 200
	s := api.New()
	if err := s.Publish(1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := false
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := s.Acquire()
			if err != nil || h.Release() != nil {
				mu.Lock()
				bad = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if bad || s.AliveCount() != 1 {
		t.Fatalf("bad=%v alive=%d", bad, s.AliveCount())
	}
}
