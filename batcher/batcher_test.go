package batcher

import (
	"testing"
	"time"
)

// fakeTimer 返回手动触发的通道，模拟注入时钟。
func fakeTimer() (NewTimer, chan time.Time) {
	fire := make(chan time.Time, 1)
	stopCalled := false
	fn := func(time.Duration) (<-chan time.Time, func()) {
		return fire, func() { stopCalled = true }
	}
	_ = stopCalled
	return fn, fire
}

func TestTriggers(t *testing.T) {
	ft, fire := fakeTimer()
	cases := []struct {
		name     string
		cfg      Config
		adds     []int
		fire     bool
		want     Reason
		wantLen  int
		wantByte int
	}{
		{"count", Config{MaxCount: 3, MaxBytes: 100, MaxWait: time.Second, NewTimer: ft}, []int{1, 1, 1}, false, Count, 3, 3},
		{"bytes", Config{MaxCount: 10, MaxBytes: 10, MaxWait: time.Second, NewTimer: ft}, []int{4, 4, 4}, false, Bytes, 3, 12},
		{"oversize-single", Config{MaxCount: 10, MaxBytes: 10, MaxWait: time.Second, NewTimer: ft}, []int{99}, false, Bytes, 1, 99},
		{"wait", Config{MaxCount: 10, MaxBytes: 100, MaxWait: time.Second, NewTimer: ft}, []int{1, 1}, true, Timer, 2, 2},
		{"zero-wait", Config{MaxCount: 10, MaxBytes: 100, MaxWait: 0}, []int{5}, false, Timer, 1, 5},
		{"count-one", Config{MaxCount: 1, MaxBytes: 100, MaxWait: time.Second, NewTimer: ft}, []int{7}, false, Count, 1, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New(tc.cfg)
			got := None
			for _, s := range tc.adds {
				got = b.Add(s)
				if got != None {
					break
				}
			}
			if got == None && tc.fire {
				fire <- time.Time{}
				<-b.Wait()
				got = Timer
			}
			if got != tc.want || b.Len() != tc.wantLen || b.Bytes() != tc.wantByte {
				t.Fatalf("got reason=%d len=%d bytes=%d, want %d %d %d",
					got, b.Len(), b.Bytes(), tc.want, tc.wantLen, tc.wantByte)
			}
			b.Reset()
			if b.Len() != 0 || b.Wait() != nil {
				t.Fatalf("reset did not clear state")
			}
		})
	}
}
