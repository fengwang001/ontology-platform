package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestNaiveReference：多种子随机 Open/Close/Recv 序列比对朴素参照（checkModel）。
func TestNaiveReference(t *testing.T) {
	for seed := uint32(1); seed <= 20; seed++ {
		if err := checkModel(seed, 500); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

// TestStaleAndHalfOpen：八步序列中旧世代帧报 ErrStale、FREE 槽帧报 ErrHalfOpen。
func TestStaleAndHalfOpen(t *testing.T) {
	d := New(4)
	for i := 0; i < 3; i++ {
		d.Open()
	}
	if err := d.Close(Handle{ID: 1, Gen: 1}); err != nil {
		t.Fatal(err)
	}
	h, err := d.Open()
	if err != nil || h != (Handle{ID: 1, Gen: 2}) {
		t.Fatalf("reuse: %+v %v", h, err)
	}
	if !errors.Is(d.Recv(1, 1, []byte("old")), ErrStale) {
		t.Fatal("stale frame not rejected")
	}
	if !errors.Is(d.Recv(3, 1, []byte("x")), ErrHalfOpen) {
		t.Fatal("half-open frame not rejected")
	}
	if err := d.Recv(1, 2, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if got := string(d.Data(1)); got != "hi" {
		t.Fatalf("cross-talk: data=%q", got)
	}
	if got := d.Data(0); len(got) != 0 {
		t.Fatalf("misdelivered to conn 0: %q", got)
	}
	d.Close(h)
	if !errors.Is(d.Recv(1, 2, []byte("y")), ErrHalfOpen) { // 关闭后未复用也是半开
		t.Fatal("closed slot not half-open")
	}
}

// TestFailureNoTrace：四类错误互不相同；被拒后状态不变，之后仍可正常使用。
func TestFailureNoTrace(t *testing.T) {
	errs := []error{ErrNoSlots, ErrBadID, ErrHalfOpen, ErrStale}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("errors %d,%d not distinct", i, j)
			}
		}
	}
	d := New(2)
	h0, _ := d.Open()
	h1, _ := d.Open()
	d.Recv(h0.ID, h0.Gen, []byte("d0"))
	snap := func() string { return fmt.Sprint(d.Gen(0), d.Gen(1), string(d.Data(0)), string(d.Data(1))) }
	before := snap()
	rejects := []error{
		d.Recv(7, 1, nil),                  // ErrBadID
		d.Close(Handle{ID: 0, Gen: 99}),    // ErrStale
		d.Send(Handle{ID: 1, Gen: 9}, nil), // ErrStale
	}
	if _, err := d.Open(); !errors.Is(err, ErrNoSlots) { // 两槽已满
		t.Fatal("want ErrNoSlots")
	}
	for _, err := range rejects {
		if err == nil {
			t.Fatal("expected rejection")
		}
	}
	if snap() != before {
		t.Fatal("rejected op mutated state")
	}
	if err := d.Recv(h1.ID, h1.Gen, []byte("ok")); err != nil || string(d.Data(1)) != "ok" {
		t.Fatal("not usable after rejections")
	}
}

// TestConcurrentNoCrossTalk：N 个 goroutine 各自 Open+Send，主线程逐连接 Recv，
// 数据一一对应不串流；(ID,Gen) 两两不同。不用 sleep 制造时序。
func TestConcurrentNoCrossTalk(t *testing.T) {
	const N = 128
	d := New(N)
	ch := make(chan Handle, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := d.Open()
			if err == nil {
				err = d.Send(h, []byte{byte(i)})
			}
			if err != nil {
				t.Errorf("open/send: %v", err)
				return
			}
			ch <- h
		}(i)
	}
	wg.Wait()
	close(ch)
	seen := map[Handle]int{}
	i := 0
	for h := range ch {
		if _, dup := seen[h]; dup {
			t.Fatalf("duplicate handle %+v", h)
		}
		seen[h] = i
		i++
	}
	if len(seen) != N {
		t.Fatalf("got %d handles, want %d", len(seen), N)
	}
	for h, i := range seen {
		if err := d.Recv(h.ID, h.Gen, []byte{byte(i)}); err != nil {
			t.Fatalf("recv: %v", err)
		}
		if got := d.Data(h.ID); string(got) != string([]byte{byte(i)}) {
			t.Fatalf("conn %+v data=%q, want marker %d", h, got, i)
		}
	}
}

// TestSelfCheck：内置自检必须通过，且可并发只读调用。
func TestSelfCheck(t *testing.T) {
	d := New(4)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.SelfCheck(); err != nil {
				t.Errorf("selfcheck: %v", err)
			}
		}()
	}
	wg.Wait()
}
