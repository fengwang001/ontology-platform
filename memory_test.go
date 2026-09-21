package ontology

import (
	"fmt"
	"testing"
)

// TestMemoryBoundIndependentOfRowCount 在一千万行输入上验证：
// 任意时刻内部持有的行数不超过 组数 × N。
func TestMemoryBoundIndependentOfRowCount(t *testing.T) {
	const (
		groupCount = 1000
		rowsPerGrp = 10000
		n          = 5
	)
	s, err := New(testConfig(n))
	if err != nil {
		t.Fatal(err)
	}

	row := map[string]any{"g": "", "s": 0.0, "t": "x"}
	total := 0
	for g := 0; g < groupCount; g++ {
		row["g"] = fmt.Sprintf("g%04d", g)
		for i := 0; i < rowsPerGrp; i++ {
			row["s"] = float64(i)
			s.Add(row)
			total++
			if total%100000 == 0 {
				held, groups := s.Stats()
				if held > groups*n {
					t.Fatalf("after %d rows: held %d > groups %d × N %d",
						total, held, groups, n)
				}
				if held > groupCount*n {
					t.Fatalf("after %d rows: held %d exceeds hard bound %d",
						total, held, groupCount*n)
				}
			}
		}
	}

	if got := s.Processed(); got != int64(groupCount*rowsPerGrp) {
		t.Fatalf("want processed %d, got %d", groupCount*rowsPerGrp, got)
	}
	held, groups := s.Stats()
	if groups != groupCount {
		t.Fatalf("want %d groups, got %d", groupCount, groups)
	}
	if held != groupCount*n {
		t.Fatalf("want exactly %d held rows, got %d", groupCount*n, held)
	}

	// 每组恰好保留分数最高的 N 行，且按分数降序。
	snap := s.Snapshot()
	for _, gs := range snap {
		if len(gs.Rows) != n {
			t.Fatalf("group %s: want %d rows, got %d", gs.Key, n, len(gs.Rows))
		}
		for i, r := range gs.Rows {
			want := float64(rowsPerGrp - 1 - i)
			if r.Score != want {
				t.Fatalf("group %s row %d: want score %v, got %v",
					gs.Key, i, want, r.Score)
			}
		}
	}
}
