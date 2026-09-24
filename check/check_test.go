package check

import (
	"context"
	"errors"
	"math/rand"
	"ontology/pmap"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestOrderAndWindow(t *testing.T) {
	for _, n := range []int{1, 4, 16} {
		for _, w := range []int{1, 4, 64} {
			ins := make([]job, 200)
			for i := range ins {
				ins[i] = job{d: time.Duration(i%3) * time.Millisecond, v: i}
			}
			got, err := run(t, context.Background(), ins, n, w)
			want, _ := Naive(context.Background(), ins, func(_ context.Context, j job) (int, error) { return j.v, j.err })
			if err != nil || !slices.Equal(got, want) || pmap.LastMaxHeld() > w {
				t.Fatalf("n=%d w=%d: err=%v got=%d held=%d", n, w, err, len(got), pmap.LastMaxHeld())
			}
		}
	}
}

func TestFailDeterministic(t *testing.T) {
	err3, err5 := errors.New("e3"), errors.New("e5")
	for it := 0; it < 200; it++ {
		failed5, cancelled := make(chan struct{}), make(chan struct{})
		ins := make([]job, 8)
		for i := range ins {
			ins[i].v = i
		}
		var got []int
		err := pmap.Run(context.Background(), ins, 4, 8, func(ctx context.Context, j job) (int, error) {
			switch j.v {
			case 5:
				close(failed5)
			case 3:
				<-failed5
				<-ctx.Done()
				close(cancelled)
			}
			return 0, map[int]error{3: err3, 5: err5}[j.v]
		}, func(i, _ int) { got = append(got, i) })
		if !errors.Is(err, err3) || errors.Is(err, err5) || !slices.Equal(got, []int{0, 1, 2}) {
			t.Fatalf("iter=%d err=%v emit=%v", it, err, got)
		}
		<-cancelled
	}
	first := make(chan error, 2) // 对照的错误实现：返回最先到达的错误
	go func() { first <- err5 }()
	go func() { time.Sleep(time.Millisecond); first <- err3 }()
	if err := <-first; !errors.Is(err, err5) {
		t.Fatalf("对照实现应返回下标 5 的错误: %v", err)
	}
}

func TestErrorsAndCancel(t *testing.T) {
	for _, c := range [][2]int{{0, 1}, {1, 0}, {-3, -5}} {
		if _, err := run(t, context.Background(), []job{{}}, c[0], c[1]); !errors.Is(err, pmap.ErrBadConfig) {
			t.Fatalf("n=%d w=%d: %v", c[0], c[1], err)
		}
	}
	boom := errors.New("boom")
	if _, err := run(t, context.Background(), []job{{}, {err: boom}}, 2, 2); !errors.Is(err, boom) {
		t.Fatalf("fn 错误应能 errors.Is 到原错误: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := run(t, func() context.Context { cancel(); return ctx }(), []job{{}}, 2, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("外部取消应返回 ctx.Err(): %v", err)
	}
}

func TestResources(t *testing.T) {
	ins := make([]job, 10000)
	for i := range ins {
		ins[i] = job{d: time.Duration(rand.Intn(20)) * time.Microsecond, v: i}
	}
	run(t, context.Background(), ins, 8, 4)
	base := runtime.NumGoroutine()
	got, err := run(t, context.Background(), ins, 8, 4)
	bad := slices.Clone(ins)
	bad[9].err = errors.New("boom")
	run(t, context.Background(), bad, 8, 4)
	if err != nil || len(got) != len(ins) {
		t.Fatalf("err=%v got=%d", err, len(got))
	}
	if !goroutinesSettled(base, 2*time.Second) {
		t.Fatalf("goroutines=%d 未回到基线 %d", runtime.NumGoroutine(), base)
	}
	t.Logf("n=8 w=4 历史最大暂存数=%d", pmap.LastMaxHeld())
	if pmap.LastMaxHeld() > 4 {
		t.Fatal("历史最大暂存数超过 w=4")
	}
}
