package mux

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// 语义 7：混合操作后计数守恒——
// Delivered + 超时数 + 关闭中断数 == 已完成的注册数，
// 且 Pending == 已注册数 - 已完成数。
func TestConservation(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.now)

	const delivered, timedOut, interrupted = 5, 3, 2
	const registered = delivered + timedOut + interrupted

	results := make(chan error, timedOut+interrupted)
	for i := 0; i < delivered; i++ {
		ch, err := m.Register(fmt.Sprintf("d%d", i))
		if err != nil {
			t.Fatal(err)
		}
		m.Deliver(fmt.Sprintf("d%d", i), []byte("ok"))
		recv(t, ch)
	}
	for i := 0; i < timedOut; i++ {
		go func(i int) {
			_, err := m.Wait(fmt.Sprintf("t%d", i), clk.now().Add(time.Minute))
			results <- err
		}(i)
	}
	for i := 0; i < interrupted; i++ {
		go func(i int) {
			_, err := m.Wait(fmt.Sprintf("c%d", i), clk.now().Add(time.Hour))
			results <- err
		}(i)
	}
	for m.Stats().Pending != timedOut+interrupted {
		time.Sleep(time.Millisecond)
	}
	clk.advance(2 * time.Minute)
	m.Tick()
	m.Close()

	gotTimedOut, gotClosed := 0, 0
	for i := 0; i < timedOut+interrupted; i++ {
		switch err := <-results; {
		case errors.Is(err, ErrTimedOut):
			gotTimedOut++
		case errors.Is(err, ErrClosed):
			gotClosed++
		default:
			t.Fatalf("unexpected err %v", err)
		}
	}
	if gotTimedOut != timedOut || gotClosed != interrupted {
		t.Fatalf("timedOut=%d closed=%d, want %d/%d", gotTimedOut, gotClosed, timedOut, interrupted)
	}

	s := m.Stats()
	completed := s.Delivered + m.timedOut + m.interrupted
	if completed != registered {
		t.Fatalf("Delivered(%d)+timedOut(%d)+interrupted(%d)=%d, want %d",
			s.Delivered, m.timedOut, m.interrupted, completed, registered)
	}
	if s.Pending != registered-completed {
		t.Fatalf("Pending=%d, want %d", s.Pending, registered-completed)
	}
}

// 语义 8：并发 Register/Deliver/Wait/Tick/Close，
// 每个响应最多派发一次，Delivered 不重复计数。需 go test -race 验证。
func TestConcurrent(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.now)

	const ids = 64
	var wg sync.WaitGroup
	received := make([][]byte, ids)
	errs := make([]error, ids)

	for i := 0; i < ids; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("id-%d", i)
			p, err := m.Wait(id, clk.now().Add(30*time.Second))
			received[i] = p
			errs[i] = err
		}(i)
	}
	for m.Stats().Pending != ids {
		time.Sleep(time.Millisecond)
	}

	// 一半派发（每个 id 派发两次，第二次必须计 Late 而非重复 Delivered）。
	for i := 0; i < ids/2; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			m.Deliver(fmt.Sprintf("id-%d", i), []byte(fmt.Sprintf("resp-%d", i)))
		}(i)
		go func(i int) {
			defer wg.Done()
			m.Deliver(fmt.Sprintf("id-%d", i), []byte(fmt.Sprintf("resp-%d", i)))
		}(i)
	}
	// 四分之一超时。
	wg.Add(1)
	go func() {
		defer wg.Done()
		clk.advance(time.Minute)
		m.Tick()
	}()
	// 剩余由 Close 中断。
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.Close()
	}()
	wg.Wait()

	for i := 0; i < ids; i++ {
		if errs[i] == nil {
			if got, want := string(received[i]), fmt.Sprintf("resp-%d", i); got != want {
				t.Fatalf("id-%d got %q, want %q", i, got, want)
			}
		} else if !errors.Is(errs[i], ErrTimedOut) && !errors.Is(errs[i], ErrClosed) {
			t.Fatalf("id-%d unexpected err %v", i, errs[i])
		}
	}

	s := m.Stats()
	if s.Pending != 0 {
		t.Fatalf("Pending=%d, want 0", s.Pending)
	}
	if s.Delivered > ids/2 {
		t.Fatalf("Delivered=%d exceeds unique deliveries %d", s.Delivered, ids/2)
	}
	if got := s.Delivered + m.timedOut + m.interrupted; got != ids {
		t.Fatalf("completed=%d, want %d", got, ids)
	}
	if _, err := m.Register("post-close"); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}
