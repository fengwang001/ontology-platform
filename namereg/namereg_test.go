package namereg

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestBasicNameRules(t *testing.T) {
	r := NewRegistry()
	r.InitShard("1")
	r.InitShard("2")
	if err := r.Hold(0, "1", "A", "neo"); err != nil {
		t.Fatal(err)
	}
	// 另一服同名互不影响。
	if err := r.Hold(0, "2", "B", "neo"); err != nil {
		t.Fatal(err)
	}
	// 同服他人占用。
	if err := r.Hold(1, "1", "C", "neo"); !errors.Is(err, ErrOccupied) {
		t.Fatalf("want occupied, got %v", err)
	}
	// 持有者本人重复持有成功。
	if err := r.Hold(2, "1", "A", "neo"); err != nil {
		t.Fatal(err)
	}
	// 离开进入保留。
	r.Detain(100, 50, "1", "A", "neo")
	if err := r.Hold(149, "1", "C", "neo"); !errors.Is(err, ErrReserved) {
		t.Fatalf("want reserved before expire, got %v", err)
	}
	// 保留者本人取回成功。
	if err := r.Hold(149, "1", "A", "neo"); err != nil {
		t.Fatalf("self reclaim: %v", err)
	}
	r.Detain(100, 50, "1", "A", "neo")
	// 取等即释放：now == 150。
	if err := r.Hold(150, "1", "C", "neo"); err != nil {
		t.Fatalf("want free at equality, got %v", err)
	}
}

// TestProbeBound 证明一次名字判定触及记录数不超过 3，且与该服角色数无关。
func TestProbeBound(t *testing.T) {
	cases := []int{100, 100000}
	var prevProbes int
	for idx, n := range cases {
		r := NewRegistry()
		r.InitShard("s")
		// 灌入 n 个角色，每人一个唯一名字。
		for i := 0; i < n; i++ {
			name := "n" + strconv.Itoa(i)
			if err := r.Hold(0, "s", "c"+strconv.Itoa(i), name); err != nil {
				t.Fatal(err)
			}
		}
		// 对一个不存在的名字做纯判定，统计触及记录数。
		r.ResetProbes()
		err := r.Check(12345, "s", "attacker", "totally-missing-name")
		if err != nil {
			t.Fatalf("missing name must be free: %v", err)
		}
		probes := r.ProbeCount()
		if probes > 3 {
			t.Fatalf("n=%d probes=%d exceeds bound 3", n, probes)
		}
		if idx > 0 && probes != prevProbes {
			t.Fatalf("probes must be scale-independent: n=%d -> %d, n=%d -> %d",
				cases[idx-1], prevProbes, n, probes)
		}
		prevProbes = probes

		// 保留到期判定同样为 O(1)：不放任何清扫，直接在 far future 查询。
		r.ResetProbes()
		start := time.Now()
		err = r.Check(int64(1_000_000_000), "s", "attacker", "n50000")
		_ = err
		if r.ProbeCount() > 3 {
			t.Fatalf("expiry check probes=%d exceeds 3", r.ProbeCount())
		}
		if time.Since(start) > time.Second {
			t.Fatal("expiry check scanned the table (too slow); full-table sweep forbidden")
		}
		t.Logf("scale n=%d: probes-per-name-check=%d (bound 3)", n, probes)
	}
	fmt.Println("probe bound verified at scales:", cases)
}
