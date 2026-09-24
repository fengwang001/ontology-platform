// batcher 触发条件与生命周期的表驱动测试。
package batcher_test

import (
	"testing"
	"time"

	"ontology/batcher"
	"ontology/req"
)

func TestTriggers(t *testing.T) {
	type row struct {
		name      string
		maxCount  int
		maxBytes  int
		wait      time.Duration
		prefire   bool // 计时 channel 在加入前就就绪（模拟 wait=0）
		fire      bool // 加入后触发超时
		sizes     []int
		wantSizes []int
		wantBytes []int
		allowOver bool
	}
	rows := []row{
		{"count", 3, 1000, time.Second, false, false,
			[]int{10, 10, 10}, []int{3}, []int{30}, false},
		{"bytes-split", 100, 100, time.Second, false, false,
			[]int{40, 40, 40}, []int{2, 1}, []int{80, 40}, false},
		{"bytes-exact", 100, 100, time.Second, false, false,
			[]int{50, 50}, []int{2}, []int{100}, false},
		{"oversize-single", 100, 100, time.Second, false, false,
			[]int{200}, []int{1}, []int{200}, true},
		{"timeout", 10, 1000, time.Second, false, true,
			[]int{10, 10, 10}, []int{3}, []int{30}, false},
		{"wait-zero", 10, 1000, 0, true, false,
			[]int{10, 10}, []int{1, 1}, []int{10, 10}, false},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan time.Time, 1)
			if tc.prefire {
				close(ch)
			}
			b := batcher.New(tc.maxCount, tc.maxBytes, tc.wait,
				func(time.Duration) <-chan time.Time { return ch })
			var gotSizes, gotBytes []int
			drain := func() {
				batch := b.Drain()
				if batch == nil {
					return
				}
				n, sum := 0, 0
				for _, r := range batch {
					n++
					sum += len(r.Payload)
				}
				gotSizes, gotBytes = append(gotSizes, n), append(gotBytes, sum)
			}
			for _, n := range tc.sizes {
				if b.FlushFirst(n) {
					drain()
				}
				b.Add(req.New(make([]byte, n)))
				if b.Full() {
					drain()
					continue
				}
				select {
				case <-b.Timer():
					drain()
				default:
				}
			}
			if tc.fire {
				ch <- time.Time{}
				<-b.Timer()
				drain()
			}
			drain()
			if len(gotSizes) != len(tc.wantSizes) {
				t.Fatalf("batches=%v want %v", gotSizes, tc.wantSizes)
			}
			for i := range gotSizes {
				if gotSizes[i] != tc.wantSizes[i] || gotBytes[i] != tc.wantBytes[i] {
					t.Fatalf("batch %d = (%d,%dB) want (%d,%dB)",
						i, gotSizes[i], gotBytes[i], tc.wantSizes[i], tc.wantBytes[i])
				}
				if !tc.allowOver && gotBytes[i] > tc.maxBytes {
					t.Fatalf("batch %d bytes %d > %d", i, gotBytes[i], tc.maxBytes)
				}
			}
		})
	}
}

func TestTimerLifecycle(t *testing.T) {
	ch := make(chan time.Time)
	b := batcher.New(3, 1000, time.Second,
		func(time.Duration) <-chan time.Time { return ch })
	if b.Timer() != nil || b.Len() != 0 || b.Bytes() != 0 {
		t.Fatal("empty batcher must have nil timer and zero size")
	}
	b.Add(req.New([]byte{1}))
	if b.Timer() == nil {
		t.Fatal("timer must arm on first item")
	}
	b.Drain()
	if b.Timer() != nil || b.Bytes() != 0 {
		t.Fatal("timer must reset after drain")
	}
}
