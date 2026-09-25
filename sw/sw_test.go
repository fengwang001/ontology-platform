package sw

import (
	"fmt"
	"testing"

	"ontology/snap"
)

// TestIncrementalO1 证明增量累加是 O(1) 的 map 更新：
// 快照规模 m 从 100 到 10000 多档，单条增量扫描的条目数不随 m 增长。
func TestIncrementalO1(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			table := make(map[string]int64, m)
			for i := 0; i < m; i++ {
				table[fmt.Sprintf("k%06d", i)] = int64(i)
			}
			s := New()
			if err := s.ApplySnapshot(snap.Snapshot{SP: 7, Table: table}); err != nil {
				t.Fatal(err)
			}
			if err := s.ApplyIncremental(snap.Event{Pos: 8, Key: "k000001", Delta: 5}); err != nil {
				t.Fatal(err)
			}
			if s.lastScan > 2 {
				t.Fatalf("m=%d: lastScan=%d, 增量疑似重扫了整张快照", m, s.lastScan)
			}
			if got := s.View()["k000001"]; got != 6 {
				t.Fatalf("m=%d: k000001=%d, want 6", m, got)
			}
		})
	}
}
