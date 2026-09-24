package slot

import (
	"fmt"
	"testing"

	"ontology/wal"
)

// 复杂度：被接受的 Confirm 重算 restart 时检查的条目数不随进行中事务数 m 线性增长。
// 计数器 checked 是非导出字段，只有包内测试能读到。
func TestCheckedIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(m + 2)
		recs := []wal.Record{{LSN: 10, Kind: wal.Begin, Xid: "T0"}}
		for i := 0; i < m; i++ {
			recs = append(recs, wal.Record{LSN: int64(20 + i), Kind: wal.Begin, Xid: fmt.Sprintf("M%d", i)})
		}
		recs = append(recs, wal.Record{LSN: int64(20 + m), Kind: wal.Commit, Xid: "T0"})
		if err := s.Append(recs...); err != nil {
			t.Fatal(err)
		}
		if err := s.Confirm(int64(20 + m)); err != nil {
			t.Fatal(err)
		}
		// 本次只有 T0 变为不需保留：检查数 = 1(弹出 T0) + 1(看队首 M0) = 2，与 m 无关
		if s.checked > 2 {
			t.Fatalf("m=%d checked=%d，随 m 线性增长", m, s.checked)
		}
		if s.RestartLSN() != 20 {
			t.Fatalf("m=%d restart=%d，期望 20（最老进行中事务 M0 的 Begin）", m, s.RestartLSN())
		}
	}
}

// 检查数 = 本次变为不需保留的条目数 + 1（队首第一个仍需保留的条目）。
func TestCheckedMatchesEvicted(t *testing.T) {
	s := New(100)
	var recs []wal.Record
	for i := 0; i < 10; i++ {
		recs = append(recs,
			wal.Record{LSN: int64(10 + 2*i), Kind: wal.Begin, Xid: fmt.Sprintf("T%d", i)},
			wal.Record{LSN: int64(11 + 2*i), Kind: wal.Commit, Xid: fmt.Sprintf("T%d", i)})
	}
	recs = append(recs, wal.Record{LSN: 100, Kind: wal.Begin, Xid: "open"})
	if err := s.Append(recs...); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(29); err != nil { // 29 是 T9 的提交 LSN
		t.Fatal(err)
	}
	// 10 个已提交且 <=29 的条目被弹出，再看 1 个队首（open@100）
	if s.checked != 11 {
		t.Fatalf("checked=%d，期望 11", s.checked)
	}
	if s.RestartLSN() != 29 {
		t.Fatalf("restart=%d，期望 29", s.RestartLSN())
	}
}
