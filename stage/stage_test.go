package stage

import (
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestStage(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"drain on close", func(t *testing.T) {
			var mu sync.Mutex
			var got []int
			st := New("a", 2, func(v int) error {
				mu.Lock()
				got = append(got, v)
				mu.Unlock()
				return nil
			})
			st.Start()
			for i := range 5 {
				if err := st.Push(i); err != nil {
					t.Fatal(err)
				}
			}
			st.Close()
			if err := st.Wait(); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(got) != 5 {
				t.Fatalf("got=%v want 5 items", got)
			}
		}},
		{"capacity one blocks producer", func(t *testing.T) {
			release := make(chan struct{})
			started := make(chan struct{}, 1)
			st := New("b", 1, func(int) error {
				select {
				case started <- struct{}{}:
				default:
				}
				<-release
				return nil
			})
			st.Start()
			if err := st.Push(1); err != nil {
				t.Fatal(err)
			}
			<-started
			pushed := make(chan struct{})
			go func() { _ = st.Push(2); close(pushed) }()
			select {
			case <-pushed:
				t.Fatal("push must block while worker is busy with capacity 1")
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			<-pushed
			st.Close()
			_ = st.Wait()
			if st.MaxInFlight() > 1 {
				t.Fatalf("max in flight=%d want <=1", st.MaxInFlight())
			}
		}},
		{"cancel before start and idempotent stop", func(t *testing.T) {
			st := New("c", 2, func(int) error { return nil })
			st.Cancel()
			st.Cancel()
			st.Start()
			if err := st.Push(1); !errors.Is(err, ErrClosed) {
				t.Fatalf("push err=%v want ErrClosed", err)
			}
			_ = st.Wait()
			st.Close()
		}},
		{"processor error surfaces", func(t *testing.T) {
			want := errors.New("boom")
			st := New("d", 2, func(int) error { return want })
			st.Start()
			_ = st.Push(1)
			if err := st.Wait(); !errors.Is(err, want) {
				t.Fatalf("wait err=%v want boom", err)
			}
			if err := st.Push(2); !errors.Is(err, ErrClosed) {
				t.Fatalf("push after fail err=%v", err)
			}
			st.Cancel()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestStageNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for range 10 {
		st := New("e", 3, func(int) error { return nil })
		st.Start()
		for i := range 9 {
			_ = st.Push(i)
		}
		st.Close()
		_ = st.Wait()
	}
	for range 50 {
		if runtime.NumGoroutine() <= base+1 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("goroutines %d above baseline %d", runtime.NumGoroutine(), base)
}
