package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// 朴素模型：存活 = 当前版本(1 个) + 仍被未释放句柄引用的历史版本数。
type model struct {
	vals, handles []int
	cur           int
}

func (m *model) alive() (n int) {
	for i := range m.vals {
		if i == m.cur || m.handles[i] > 0 {
			n++
		}
	}
	return
}

type lh struct {
	vid int
	h   *api.Handle
}

func TestNaiveConsistency(t *testing.T) {
	for _, tc := range []struct {
		seed int64
		ops  int
	}{{1, 200}, {7, 500}, {42, 1000}} {
		t.Run("", func(t *testing.T) {
			rng, d, m := rand.New(rand.NewSource(tc.seed)), api.New(), &model{cur: -1}
			var live []lh
			for i := 0; i < tc.ops; i++ {
				switch rng.Intn(3) {
				case 0:
					v := rng.Intn(1000)
					must(t, d.Publish(v))
					m.vals, m.handles = append(m.vals, v), append(m.handles, 0)
					m.cur = len(m.vals) - 1
				case 1:
					if m.cur >= 0 {
						h, err := d.Acquire()
						must(t, err)
						m.handles[m.cur]++
						live = append(live, lh{m.cur, h})
					}
				case 2:
					if len(live) > 0 {
						j := rng.Intn(len(live))
						must(t, live[j].h.Release())
						m.handles[live[j].vid]--
						live = append(live[:j], live[j+1:]...)
					}
				}
				if got := d.AliveCount(); got != m.alive() {
					t.Fatalf("op %d: alive=%d want %d", i, got, m.alive())
				}
			}
			for _, l := range live { // 每个未释放句柄 Get 返回其发布值
				if v, err := l.h.Get(); err != nil || v != m.vals[l.vid] {
					t.Fatalf("get=%v,%v want %d", v, err, m.vals[l.vid])
				}
			}
		})
	}
}

func TestFailureNoSideEffect(t *testing.T) {
	cases := []struct {
		name string
		op   func(r, l *api.Handle, d *api.DB) error
		want error
	}{
		{"double-release", func(r, l *api.Handle, d *api.DB) error { return r.Release() }, api.ErrDoubleRelease},
		{"use-after-free", func(r, l *api.Handle, d *api.DB) error { _, e := r.Get(); return e }, api.ErrUseAfterFree},
		{"negative-publish", func(r, l *api.Handle, d *api.DB) error { return d.Publish(-1) }, api.ErrNegativeValue},
		{"empty-acquire", func(r, l *api.Handle, d *api.DB) error { _, e := api.New().Acquire(); return e }, api.ErrEmptyStore},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := api.New()
			must(t, d.Publish(1))
			r, _ := d.Acquire()
			l, _ := d.Acquire()
			must(t, d.Publish(2))
			must(t, r.Release())
			beforeAlive, beforeRefs := d.AliveCount(), l.Refs()
			if err := tc.op(r, l, d); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if d.AliveCount() != beforeAlive || l.Refs() != beforeRefs {
				t.Fatal("rejected op changed state")
			}
			must(t, l.Release()) // 被拒后仍可正常使用
		})
	}
}

func TestConcurrentAcquireRelease(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		t.Run("", func(t *testing.T) {
			d := api.New()
			must(t, d.Publish(1))
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(2)
				go func() {
					defer wg.Done()
					h, err := d.Acquire()
					if err != nil {
						t.Error(err)
						return
					}
					if _, err := h.Get(); err != nil {
						t.Error(err)
					}
					if err := h.Release(); err != nil {
						t.Error(err)
					}
					_ = d.AliveCount()
				}()
				go func() { defer wg.Done(); _ = api.New().SelfCheck() }()
			}
			wg.Wait()
			if got := d.AliveCount(); got != 1 {
				t.Fatalf("alive=%d want 1", got)
			}
		})
	}
}
