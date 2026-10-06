package part

import (
	"errors"
	"fmt"
	"testing"
)

func TestCommitAndWatermark(t *testing.T) {
	tb := NewTable()
	if tb.W() != -1 {
		t.Fatalf("initial W = %d, want -1", tb.W())
	}
	// 乱序提交制造空洞：W 停在 0 之前。
	for _, p := range []int{1, 3, 2} {
		if err := tb.Commit(p); err != nil {
			t.Fatalf("Commit(%d): %v", p, err)
		}
	}
	if tb.W() != -1 {
		t.Fatalf("W = %d, want -1 (0 missing)", tb.W())
	}
	if err := tb.Commit(0); err != nil {
		t.Fatal(err)
	}
	if tb.W() != 3 {
		t.Fatalf("W = %d, want 3", tb.W())
	}
	for p := 0; p <= 3; p++ {
		if tb.Ver(p) != 1 {
			t.Fatalf("Ver(%d) = %d, want 1", p, tb.Ver(p))
		}
	}
	if tb.Ver(4) != 0 || tb.Committed(4) {
		t.Fatal("partition 4 should be Missing")
	}
	if err := tb.Commit(2); !errors.Is(err, ErrAlready) {
		t.Fatalf("re-commit err = %v, want ErrAlready", err)
	}
}

// probes 界：单次 Commit/BumpRange 探测数 <= (新W - 旧W) + 1，
// 与已提交分区总数无关。100 与 100000 两档。
func TestProbesBound(t *testing.T) {
	for _, n := range []int{100, 100000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			tb := NewTable()
			// 先提交最远的分区制造空洞：W 不动，probes 应为 1。
			if err := tb.Commit(n - 1); err != nil {
				t.Fatal(err)
			}
			if tb.probes != 1 {
				t.Fatalf("probes = %d, want 1 (W unchanged)", tb.probes)
			}
			prevW := tb.W()
			for p := 0; p < n-1; p++ {
				if err := tb.Commit(p); err != nil {
					t.Fatal(err)
				}
				if delta := tb.W() - prevW; tb.probes > delta+1 {
					t.Fatalf("p=%d: probes=%d > (newW-oldW)+1=%d", p, tb.probes, delta+1)
				}
				prevW = tb.W()
			}
			if tb.W() != n-1 {
				t.Fatalf("W = %d, want %d", tb.W(), n-1)
			}
		})
	}
}

func TestBumpRange(t *testing.T) {
	tb := NewTable()
	// Missing 视为 0，BumpRange 后为 1。
	tb.BumpRange(0, 2)
	for p := 0; p <= 2; p++ {
		if tb.Ver(p) != 1 {
			t.Fatalf("Ver(%d) = %d, want 1", p, tb.Ver(p))
		}
	}
	if tb.W() != 2 {
		t.Fatalf("W = %d, want 2", tb.W())
	}
	// 再次回填：版本加 1；probes 满足界。
	tb.BumpRange(1, 3)
	if tb.Ver(1) != 2 || tb.Ver(3) != 1 {
		t.Fatalf("Ver(1)=%d Ver(3)=%d, want 2 and 1", tb.Ver(1), tb.Ver(3))
	}
	if tb.W() != 3 {
		t.Fatalf("W = %d, want 3", tb.W())
	}
	if tb.probes > 2 { // ΔW=1 -> 界为 2
		t.Fatalf("probes = %d, want <= 2", tb.probes)
	}
}
