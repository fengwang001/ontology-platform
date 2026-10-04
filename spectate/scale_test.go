package spectate

import (
	"testing"

	"ontology/live"
)

// TestLagTouchedScale 证明一次 Lag 的读取不随事件总数 n 线性增长:
// n=10^3 与 n=10^6 两档, 完整记录读取恒为 0, 索引探针恒 <=64。
func TestLagTouchedScale(t *testing.T) {
	for _, n := range []int{1_000, 1_000_000} {
		s, err := New(0, 10)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= n; i++ {
			kind := live.Normal
			if i%7 == 0 {
				kind = live.Hidden
			}
			if _, err := s.Emit(int64(i), kind); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Emit(int64(n)+1, live.End); err != nil {
			t.Fatal(err)
		}
		if err := s.Join(int64(n)+2, "v", false); err != nil {
			t.Fatal(err)
		}
		lag, touch, err := s.Lag(int64(n)+2, "v")
		if err != nil {
			t.Fatal(err)
		}
		if lag != n+1 {
			t.Fatalf("n=%d lag=%d want %d", n, lag, n+1)
		}
		if touch.Records != 0 || touch.Probes > 64 {
			t.Fatalf("n=%d Lag reads records=%d probes=%d, want 0 records and <=64 probes",
				n, touch.Records, touch.Probes)
		}

		// Pull 返回 1 条时读取记录数 <= 1 + 0 + 1 = 2, 与 n 无关。
		res, ptouch, err := s.Pull(int64(n)+2, "v", 1)
		if err != nil || len(res.Events) != 1 || !res.More {
			t.Fatalf("n=%d pull got %d more=%v err=%v", n, len(res.Events), res.More, err)
		}
		if ptouch.Records > 2 {
			t.Fatalf("n=%d Pull touched %d records, bound 2", n, ptouch.Records)
		}
	}
}
