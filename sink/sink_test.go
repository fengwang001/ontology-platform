package sink

import (
	"errors"
	"testing"

	"ontology/wm"
)

// TestRebuildScanBounded：4 分区 3 Key，m 取多档，重启检查条目数
// 必须恒等于分区数+Key 数且不随 m 线性增长——水位是直接读回而非重放。
func TestRebuildScanBounded(t *testing.T) {
	const P, K = 4, 3
	const bound = P + K + 2
	ms := []int{100, 500, 1000, 5000, 10000}
	prev := -1
	for _, m := range ms {
		s := New()
		next := make([]int64, P)
		for n := 0; n < m; {
			b := make([]wm.Rec, 0, 64)
			for len(b) < 64 && n < m {
				p := n % P
				b = append(b, wm.Rec{Partition: p, Offset: next[p], Key: string(rune('x' + n%K)), Val: 1})
				next[p]++
				n++
			}
			if err := s.Commit(b, P); err != nil {
				t.Fatalf("m=%d commit: %v", m, err)
			}
		}
		s.Restart()
		got := s.rebuildScanned // 直接读非导出字段，不走任何导出方法
		if got > bound {
			t.Fatalf("m=%d scanned=%d > bound %d", m, got, bound)
		}
		if prev >= 0 && got != prev {
			t.Fatalf("scanned grew with m: %d -> %d", prev, got)
		}
		prev = got
	}
}

// TestCommitValidation：三类拒绝错误可判定、互不相同，且整批不留痕、之后可用。
func TestCommitValidation(t *testing.T) {
	cases := []struct {
		name  string
		maxP  int
		batch []wm.Rec
		want  error
	}{
		{"illegal-negative-partition", 8, []wm.Rec{{Partition: -1, Offset: 0, Key: "a", Val: 1}}, wm.ErrIllegalRec},
		{"illegal-negative-offset", 8, []wm.Rec{{Partition: 0, Offset: -1, Key: "a", Val: 1}}, wm.ErrIllegalRec},
		{"illegal-empty-key", 8, []wm.Rec{{Partition: 0, Offset: 0, Key: "", Val: 1}}, wm.ErrIllegalRec},
		{"illegal-before-outoforder", 8, []wm.Rec{
			{Partition: 0, Offset: 2, Key: "a", Val: 1}, {Partition: 0, Offset: 2, Key: "a", Val: 1}, {Partition: -1, Offset: 0, Key: "", Val: 1},
		}, wm.ErrIllegalRec},
		{"outoforder-equal", 8, []wm.Rec{
			{Partition: 0, Offset: 1, Key: "a", Val: 1}, {Partition: 0, Offset: 1, Key: "b", Val: 1},
		}, wm.ErrOutOfOrder},
		{"outoforder-decreasing", 8, []wm.Rec{
			{Partition: 2, Offset: 3, Key: "a", Val: 1}, {Partition: 2, Offset: 2, Key: "b", Val: 1},
		}, wm.ErrOutOfOrder},
		{"too-many-partitions", 1, []wm.Rec{
			{Partition: 0, Offset: 0, Key: "a", Val: 1}, {Partition: 1, Offset: 0, Key: "b", Val: 1},
		}, ErrTooManyPartitions},
	}
	errs := map[error]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			if err := s.Commit(tc.batch, tc.maxP); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			errs[tc.want] = true
			if len(s.Table()) != 0 || s.Duplicates() != 0 {
				t.Fatalf("rejected batch left state: %v dups=%d", s.Table(), s.Duplicates())
			}
		})
	}
	if len(errs) != 3 {
		t.Fatalf("three error kinds must be distinct, got %d", len(errs))
	}
}
