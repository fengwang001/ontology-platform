package bulkhead_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/bulkhead"
)

func TestBulkhead(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"invalid-config", func(t *testing.T) {
			for _, cfg := range [][2]int{{0, 0}, {-1, 2}, {2, -1}} {
				if _, err := bulkhead.New(cfg[0], cfg[1]); !errors.Is(err, bulkhead.ErrInvalidConfig) {
					t.Fatalf("New(%v)=%v want ErrInvalidConfig", cfg, err)
				}
			}
		}},
		{"release-restores-capacity", func(t *testing.T) {
			b, _ := bulkhead.New(2, 0)
			r1, err := b.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			r2, err := b.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if b.Available() != 0 {
				t.Fatalf("available=%d want 0", b.Available())
			}
			if _, err := b.Acquire(context.Background()); !errors.Is(err, bulkhead.ErrBulkheadFull) {
				t.Fatalf("want full, got %v", err)
			}
			r1()
			r2()
			r1() // 重复释放必须幂等
			if b.Available() != 2 {
				t.Fatalf("available=%d want 2", b.Available())
			}
		}},
		{"full-rejected-without-waiting", func(t *testing.T) {
			b, _ := bulkhead.New(1, 0)
			r, _ := b.Acquire(context.Background())
			defer r()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := b.Acquire(ctx)
			if !errors.Is(err, bulkhead.ErrBulkheadFull) {
				t.Fatalf("want full, got %v", err)
			}
			if time.Since(start) > 10*time.Millisecond {
				t.Fatalf("rejection took %v, must be immediate", time.Since(start))
			}
		}},
		{"cancel-while-queued", func(t *testing.T) {
			b, _ := bulkhead.New(1, 2)
			holder, _ := b.Acquire(context.Background())
			ctx1, cancel1 := context.WithCancel(context.Background())
			ctx2, cancel2 := context.WithCancel(context.Background())
			defer cancel2()
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for _, c := range []context.Context{ctx1, ctx2} {
				wg.Add(1)
				go func(cc context.Context) {
					defer wg.Done()
					rel, err := b.Acquire(cc)
					if err == nil {
						rel()
					}
					errs <- err
				}(c)
			}
			time.Sleep(20 * time.Millisecond)
			cancel1()
			time.Sleep(20 * time.Millisecond)
			holder()
			wg.Wait()
			close(errs)
			sawCancel, sawOK := false, false
			for e := range errs {
				sawCancel = sawCancel || errors.Is(e, bulkhead.ErrBulkheadCanceled)
				sawOK = sawOK || e == nil
			}
			if !sawCancel || !sawOK {
				t.Fatalf("cancel=%v ok=%v", sawCancel, sawOK)
			}
			if b.Available() != 1 {
				t.Fatalf("available=%d want 1 after cancel+drain", b.Available())
			}
		}},
		{"peak-capped-at-8-with-500", func(t *testing.T) {
			b, _ := bulkhead.New(8, 500)
			const gor = 500
			gate := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < gor; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-gate
					rel, err := b.Acquire(context.Background())
					if err != nil {
						t.Errorf("acquire: %v", err)
						return
					}
					time.Sleep(time.Millisecond)
					rel()
				}()
			}
			close(gate)
			wg.Wait()
			if p := b.Peak(); p > 8 {
				t.Fatalf("peak=%d > 8", p)
			}
			if b.Available() != 8 {
				t.Fatalf("available=%d want 8", b.Available())
			}
		}},
		{"n1-serial", func(t *testing.T) {
			b, _ := bulkhead.New(1, 100)
			var wg sync.WaitGroup
			gate := make(chan struct{})
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-gate
					rel, err := b.Acquire(context.Background())
					if err != nil {
						t.Errorf("acquire: %v", err)
						return
					}
					rel()
				}()
			}
			close(gate)
			wg.Wait()
			if b.Peak() != 1 || b.Available() != 1 {
				t.Fatalf("peak=%d available=%d", b.Peak(), b.Available())
			}
		}},
		{"independent-downstreams", func(t *testing.T) {
			x, _ := bulkhead.New(1, 0)
			y, _ := bulkhead.New(2, 0)
			rx, _ := x.Acquire(context.Background())
			defer rx()
			if _, err := x.Acquire(context.Background()); !errors.Is(err, bulkhead.ErrBulkheadFull) {
				t.Fatalf("x should be full")
			}
			if y.Available() != 2 {
				t.Fatalf("y available=%d want 2", y.Available())
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
