package mux

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func recv(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delivery")
		return nil
	}
}

// 语义 1：一对一派发，payload 原样交付且与调用方切片隔离。
func TestDeliverOneToOne(t *testing.T) {
	m := New(nil)
	ch, err := m.Register("a")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello")
	m.Deliver("a", payload)
	payload[0] = 'X' // 改写原切片不得影响已派发内容
	got := recv(t, ch)
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
	s := m.Stats()
	if s.Pending != 0 || s.Delivered != 1 {
		t.Fatalf("stats = %+v, want Pending=0 Delivered=1", s)
	}
}

// 语义 2：重复 id 拒绝，且不影响已有等待者；完成后可重新注册。
func TestDuplicateID(t *testing.T) {
	m := New(nil)
	ch, err := m.Register("a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Register("a"); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
	m.Deliver("a", []byte("first"))
	if got := recv(t, ch); string(got) != "first" {
		t.Fatalf("original waiter got %q", got)
	}
	ch2, err := m.Register("a")
	if err != nil {
		t.Fatalf("re-register after completion: %v", err)
	}
	m.Deliver("a", []byte("second"))
	if got := recv(t, ch2); string(got) != "second" {
		t.Fatalf("got %q", got)
	}
}

// 语义 3：孤儿响应被丢弃，不为它创建等待槽。
func TestOrphanDropped(t *testing.T) {
	m := New(nil)
	m.Deliver("ghost", []byte("orphan"))
	s := m.Stats()
	if s.Orphans != 1 || s.Pending != 0 {
		t.Fatalf("stats = %+v, want Orphans=1 Pending=0", s)
	}
	ch, err := m.Register("ghost")
	if err != nil {
		t.Fatal(err)
	}
	m.Deliver("ghost", []byte("real"))
	if got := recv(t, ch); string(got) != "real" {
		t.Fatalf("got %q, want the second payload", got)
	}
	if s := m.Stats(); s.Delivered != 1 || s.Orphans != 1 {
		t.Fatalf("stats = %+v", s)
	}
}

// 语义 4：迟到响应计 Late 而非 Orphans。
func TestLateResponse(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.now)
	done := make(chan error, 1)
	go func() {
		_, err := m.Wait("a", clk.now().Add(time.Second))
		done <- err
	}()
	for m.Stats().Pending != 1 {
		time.Sleep(time.Millisecond)
	}
	clk.advance(2 * time.Second)
	m.Tick()
	if err := <-done; !errors.Is(err, ErrTimedOut) {
		t.Fatalf("err = %v, want ErrTimedOut", err)
	}
	m.Deliver("a", []byte("too late"))
	s := m.Stats()
	if s.Late != 1 || s.Orphans != 0 {
		t.Fatalf("stats = %+v, want Late=1 Orphans=0", s)
	}
}

// 语义 5：超时账目——ErrTimedOut、Pending 递减、不影响 Delivered/Orphans。
func TestTimeoutAccounting(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.now)
	done := make(chan error, 1)
	go func() {
		_, err := m.Wait("a", clk.now().Add(time.Second))
		done <- err
	}()
	for m.Stats().Pending != 1 {
		time.Sleep(time.Millisecond)
	}
	clk.advance(time.Second)
	m.Tick()
	if err := <-done; !errors.Is(err, ErrTimedOut) {
		t.Fatalf("err = %v, want ErrTimedOut", err)
	}
	s := m.Stats()
	if s.Pending != 0 || s.Delivered != 0 || s.Orphans != 0 || s.Late != 0 {
		t.Fatalf("stats = %+v, want all zero", s)
	}
}

// 语义 6：Close 的账目与幂等。
func TestClose(t *testing.T) {
	m := New(nil)
	ch, err := m.Register("a")
	if err != nil {
		t.Fatal(err)
	}
	waitErr := make(chan error, 1)
	go func() {
		_, err := m.Wait("b", time.Now().Add(time.Hour))
		waitErr <- err
	}()
	for m.Stats().Pending != 2 {
		time.Sleep(time.Millisecond)
	}
	m.Close()
	m.Close() // 幂等，不得 panic
	if err := <-waitErr; !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be drained via done, not receive payload")
		}
	case <-time.After(50 * time.Millisecond):
		// Register 的 channel 在 Close 时不发送值，符合预期
	}
	if _, err := m.Register("c"); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	m.Deliver("a", []byte("late"))  // 曾注册过 → Late
	m.Deliver("zz", []byte("orph")) // 从未注册 → Orphans
	s := m.Stats()
	if s.Pending != 0 || s.Late != 1 || s.Orphans != 1 || s.Delivered != 0 {
		t.Fatalf("stats = %+v", s)
	}
}
