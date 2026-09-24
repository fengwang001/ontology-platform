package batcher

import (
	"errors"
	"testing"
	"time"

	"ontology/req"
)

type fakeTimer struct {
	c    chan time.Time
	stop bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }
func (t *fakeTimer) Stop() bool {
	was := !t.stop
	t.stop = true
	return was
}

type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) NewTimer(d time.Duration) Timer {
	ft := &fakeTimer{c: make(chan time.Time, 1)}
	f.timers = append(f.timers, ft)
	return ft
}

func (f *fakeClock) advance(d time.Duration) {
	f.now = f.now.Add(d)
	timers := f.timers
	f.timers = nil
	for _, ft := range timers {
		if !ft.stop {
			ft.c <- f.now
		}
	}
}

func mkItem(size int) req.Pending {
	return req.NewPending(req.Request{Payload: make([]byte, size)})
}

func drainBatches(b *Batcher) []Batch {
	var got []Batch
	for batch := range b.Batches() {
		got = append(got, batch)
	}
	return got
}

func TestTriggers(t *testing.T) {
	submit := func(b *Batcher, sizes ...int) {
		for _, s := range sizes {
			if err := b.Submit(mkItem(s)); err != nil {
				t.Fatalf("submit: %v", err)
			}
		}
	}
	cases := []struct {
		name      string
		cfg       Config
		act       func(b *Batcher, c *fakeClock)
		wantSizes []int
		wantBytes []int
	}{
		{"count", Config{MaxCount: 3, MaxBytes: 1000, MaxWait: time.Hour},
			func(b *Batcher, _ *fakeClock) { submit(b, 1, 1, 1, 1, 1, 1) },
			[]int{3, 3}, []int{3, 3}},
		{"bytes", Config{MaxCount: 100, MaxBytes: 10, MaxWait: time.Hour},
			func(b *Batcher, _ *fakeClock) { submit(b, 4, 6, 5, 5, 10) },
			[]int{2, 2, 1}, []int{10, 10, 10}},
		{"timeout", Config{MaxCount: 100, MaxBytes: 1000, MaxWait: 50 * time.Millisecond},
			func(b *Batcher, c *fakeClock) {
				submit(b, 1, 1)
				c.advance(50 * time.Millisecond)
				submit(b, 1)
				c.advance(50 * time.Millisecond)
			},
			[]int{2, 1}, []int{2, 1}},
		{"nowait", Config{MaxCount: 100, MaxBytes: 1000, MaxWait: 0},
			func(b *Batcher, _ *fakeClock) { submit(b, 1, 1, 1) },
			[]int{1, 1, 1}, []int{1, 1, 1}},
		{"oversize", Config{MaxCount: 100, MaxBytes: 10, MaxWait: time.Hour},
			func(b *Batcher, _ *fakeClock) { submit(b, 1, 25, 1) },
			[]int{1, 1, 1}, []int{1, 25, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeClock()
			b := New(tc.cfg, c)
			result := make(chan []Batch, 1)
			go func() { result <- drainBatches(b) }()
			tc.act(b, c)
			b.Close()
			got := <-result
			if len(got) != len(tc.wantSizes) {
				t.Fatalf("%s: 批次数=%d, 想=%d", tc.name, len(got), len(tc.wantSizes))
			}
			maxBytes := 0
			for i, batch := range got {
				if len(batch.Items) != tc.wantSizes[i] || batch.Bytes != tc.wantBytes[i] {
					t.Fatalf("%s 批%d 大小=%d 字节=%d, 想 %d/%d",
						tc.name, i, len(batch.Items), batch.Bytes, tc.wantSizes[i], tc.wantBytes[i])
				}
				if tc.name != "oversize" && batch.Bytes > tc.cfg.MaxBytes {
					t.Fatalf("%s 批%d 字节 %d 越界", tc.name, i, batch.Bytes)
				}
				if batch.Bytes > maxBytes {
					maxBytes = batch.Bytes
				}
			}
			if tc.name == "bytes" && maxBytes != 10 {
				t.Fatalf("历史最大批字节=%d, 想 10", maxBytes)
			}
		})
	}
}

func TestCloseAndEmpty(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		cfg   Config
		wantN int
	}{
		{"empty", 0, Config{MaxCount: 1, MaxBytes: 1, MaxWait: 0}, 0},
		{"single", 1, Config{MaxCount: 1, MaxBytes: 1, MaxWait: 0}, 1},
		{"countone", 5, Config{MaxCount: 1, MaxBytes: 100, MaxWait: time.Hour}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New(tc.cfg, newFakeClock())
			result := make(chan []Batch, 1)
			go func() { result <- drainBatches(b) }()
			for range tc.n {
				if err := b.Submit(mkItem(1)); err != nil {
					t.Fatalf("submit: %v", err)
				}
	}
			b.Close()
			got := <-result
			total := 0
			for _, batch := range got {
				total += len(batch.Items)
			}
			if total != tc.wantN {
				t.Fatalf("收到 %d 条, 想 %d", total, tc.wantN)
			}
			if err := b.Submit(mkItem(1)); !errors.Is(err, ErrClosed) {
				t.Fatalf("Close 后 Submit 错误=%v, 想 ErrClosed", err)
			}
		})
	}
}
