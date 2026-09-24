package dedup

import (
	"fmt"
	"strconv"
	"testing"
)

// TestScanSublinear 钉住复杂度约束：检查条数不随记忆条数 m 线性增长，
// 且恰好等于「本次清除条数 + 1」（按过期点有序定位，非整表扫描）。
func TestScanSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		d := New(1<<60, 2)
		for i := 0; i < m; i++ {
			d.Process("id-"+strconv.Itoa(i), 1_000_000+int64(i)) // 首见 TS 足够大
		}
		d.Process("probe", 1_000_000+int64(m)) // wm 恰前进 1，无记忆过期
		if d.scan > 4 {
			t.Fatalf("m=%d: scan=%d, want a small constant independent of m", m, d.scan)
		}
	}
	for _, k := range []int{1, 10, 40} { // 一次清除 k 条时 scan = k+1
		d := New(10, 0)
		for i := 0; i < 50; i++ {
			d.add("id-"+strconv.Itoa(i), int64(i))
		}
		d.purge(int64(k + 9)) // 过期点 firstTS+10 <= wm → 恰清 firstTS 0..k-1 共 k 条
		want := k + 1
		if d.scan != want {
			t.Fatalf("k=%d: scan=%d, want %d (evicted+1)", k, d.scan, want)
		}
		if d.Len() != 50-k {
			t.Fatalf("k=%d: Len=%d, want %d", k, d.Len(), 50-k)
		}
	}
}

// TestProcessTable 把 NOTES.md 的十行分步表逐步钉死（ttl=10, delay=2）。
func TestProcessTable(t *testing.T) {
	steps := []struct {
		id  string
		ts  int64
		wm  int64
		dup bool
		mem string
		nd  int64
	}{
		{"a", 5, 3, false, "map[a:5]", 0},
		{"b", 8, 6, false, "map[a:5 b:8]", 0},
		{"a", 9, 7, true, "map[a:5 b:8]", 1},
		{"c", 17, 15, false, "map[b:8 c:17]", 1},       // 边界：15=5+10 恰清 a
		{"a", 14, 15, false, "map[a:14 b:8 c:17]", 1},  // a 已过期清除 → 新
		{"b", 20, 18, false, "map[a:14 b:20 c:17]", 1}, // 边界：18=8+10 恰清 b
		{"d", 4, 18, false, "map[a:14 b:20 c:17]", 1},  // 即记即清
		{"d", 6, 18, false, "map[a:14 b:20 c:17]", 1},  // 上步已清 → 仍是新
		{"c", 26, 24, true, "map[b:20 c:17]", 2},       // 24=14+10 恰清 a；c 未过期
		{"a", 25, 24, false, "map[a:25 b:20 c:17]", 2},
	}
	d := New(10, 2)
	for i, s := range steps {
		if dup := d.Process(s.id, s.ts); dup != s.dup {
			t.Fatalf("step %d (%s,%d): dup=%v, want %v", i+1, s.id, s.ts, dup, s.dup)
		}
		if wm := d.WM(); wm != s.wm {
			t.Fatalf("step %d: wm=%d, want %d", i+1, wm, s.wm)
		}
		if m := fmt.Sprint(d.Mem()); m != s.mem {
			t.Fatalf("step %d: mem=%s, want %s", i+1, m, s.mem)
		}
		if d.Dups() != s.nd {
			t.Fatalf("step %d: dups=%d, want %d", i+1, d.Dups(), s.nd)
		}
	}
}
