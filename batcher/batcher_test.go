package batcher

import (
	"testing"
	"time"

	"ontology/req"
)

// fakeClock 按调用次序返回可手动投递的定时器，保证确定触发。
type fakeClock struct {
	timers []chan time.Time
}

func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.timers = append(f.timers, ch)
	return ch
}

func (f *fakeClock) fire(i int) { f.timers[i] <- time.Time{} }

func collect(b *Batcher) []*Batch {
	var out []*Batch
	for x := range b.Out() {
		out = append(out, x)
	}
	return out
}

func payload(n int) []byte { return make([]byte, n) }

func TestBatcherTriggers(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		act     func(b *Batcher, c *fakeClock)
		wantLens  []int
		wantBytes []int
	}{
		{
			name: "count",
			cfg:  Config{MaxCount: 3, MaxBytes: 1000, MaxWait: time.Hour},
			act: func(b *Batcher, c *fakeClock) {
				for i := 0; i < 7; i++ {
					_ = b.Submit(req.New(nil))
				}
				b.Close()
			},
			wantLens:  []int{3, 3, 1},
			wantBytes: []int{0, 0, 0},
		},
		{
			name: "bytes",
			cfg:  Config{MaxCount: 100, MaxBytes: 10, MaxWait: time.Hour},
			act: func(b *Batcher, c *fakeClock) {
				_ = b.Submit(req.New(payload(4)))
				_ = b.Submit(req.New(payload(6))) // 累计 10 触发
				b.Close()
			},
			wantLens:  []int{2},
			wantBytes: []int{10},
		},
		{
			name: "timeout",
			cfg:  Config{MaxCount: 100, MaxBytes: 1000, MaxWait: time.Minute},
			act: func(b *Batcher, c *fakeClock) {
				_ = b.Submit(req.New(nil))
				_ = b.Submit(req.New(nil))
				c.fire(0) // 等待超时 -> 2 条成批
				b.Close()
			},
			wantLens:  []int{2},
			wantBytes: []int{0},
		},
		{
			name: "oversize_single",
			cfg:  Config{MaxCount: 100, MaxBytes: 10, MaxWait: time.Hour},
			act: func(b *Batcher, c *fakeClock) {
				_ = b.Submit(req.New(payload(50))) // 超大单条单独成批
				b.Close()
			},
			wantLens:  []int{1},
			wantBytes: []int{50},
		},
		{
			name: "count_one",
			cfg:  Config{MaxCount: 1, MaxBytes: 1, MaxWait: 0},
			act: func(b *Batcher, c *fakeClock) {
				_ = b.Submit(req.New(payload(3)))
				_ = b.Submit(req.New(nil))
				b.Close()
			},
			wantLens:  []int{1, 1},
			wantBytes: []int{3, 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{}
			tc.cfg.Clock = clk
			b := New(tc.cfg)
			done := make(chan []*Batch, 1)
			go func() { done <- collect(b) }()
			tc.act(b, clk)
			got := <-done
			if len(got) != len(tc.wantLens) {
				t.Fatalf("batches=%d want=%d", len(got), len(tc.wantLens))
			}
			for i := range got {
				if len(got[i].Entries) != tc.wantLens[i] || got[i].Bytes != tc.wantBytes[i] {
					t.Fatalf("batch %d=(n=%d bytes=%d) want n=%d bytes=%d",
						i, len(got[i].Entries), got[i].Bytes, tc.wantLens[i], tc.wantBytes[i])
				}
			}
		})
	}
}

func TestSubmitAfterClose(t *testing.T) {
	b := New(Config{MaxCount: 10, MaxBytes: 100, MaxWait: time.Hour})
	b.Close()
	if err := b.Submit(req.New(nil)); err != req.ErrClosed {
		t.Fatalf("err=%v want ErrClosed", err)
	}
}
