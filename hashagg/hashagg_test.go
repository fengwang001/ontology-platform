package hashagg

import (
	"fmt"
	"math"
	"testing"

	"ontology/acc"
	"ontology/row"
	"ontology/verify"
)

func runAgg(t *testing.T, numParts, budget int, rows []row.Row) ([]acc.Group, Stats) {
	t.Helper()
	a, err := New(numParts, budget, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := a.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	groups, err := a.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return groups, a.Stats()
}

func TestEdgeSemantics(t *testing.T) {
	cases := []struct {
		name        string
		budget      int
		rows        []row.Row
		wantSpills  int // -1 表示不检查
		wantSkipped int64
	}{
		{"零行", 4, nil, 0, 0},
		{"单行", 4, []row.Row{{Key: "a", Val: 1}}, 0, 0},
		{"所有行同一分组", 2, repeat("same", 100), 0, 0},
		{"分组数等于预算不溢出", 4, distinct(4), 0, 0},
		{"分组数等于预算加一溢出一次", 4, distinct(5), 1, 0},
		{"空串键合法", 4, []row.Row{{Key: "", Val: 7}, {Key: "", Val: 8}}, 0, 0},
		{"NaN拒绝并计数", 4, []row.Row{{Key: "a", Val: 1}, {Key: "a", Val: math.NaN()}, {Key: "b", Val: math.NaN()}, {Key: "a", Val: 2}}, 0, 2},
		{"正负Inf参与聚合", 4, []row.Row{{Key: "k", Val: math.Inf(1)}, {Key: "k", Val: 1}, {Key: "j", Val: math.Inf(-1)}, {Key: "j", Val: 2}}, 0, 0},
		{"正负零视为相等", 4, []row.Row{{Key: "k", Val: 0.0}, {Key: "k", Val: math.Copysign(0, -1)}}, 0, 0},
	}
	for _, c := range cases {
		groups, st := runAgg(t, 4, c.budget, c.rows)
		if err := verify.CompareGroups(groups, verify.InMemory(c.rows)); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.wantSpills >= 0 && st.Spills != c.wantSpills {
			t.Errorf("%s: 溢出次数=%d, 期望 %d", c.name, st.Spills, c.wantSpills)
		}
		if st.Skipped != c.wantSkipped {
			t.Errorf("%s: 跳过数=%d, 期望 %d", c.name, st.Skipped, c.wantSkipped)
		}
	}
}

func repeat(key string, n int) []row.Row {
	rows := make([]row.Row, n)
	for i := range rows {
		rows[i] = row.Row{Key: key, Val: float64(i)}
	}
	return rows
}

func distinct(n int) []row.Row {
	rows := make([]row.Row, n)
	for i := range rows {
		rows[i] = row.Row{Key: fmt.Sprintf("k%d", i), Val: float64(i)}
	}
	return rows
}

func TestPeakAndCounters(t *testing.T) {
	const n, budget = 50000, 500
	groups, st := runAgg(t, 16, budget, distinct(n))
	if st.PeakResident > budget {
		t.Errorf("驻留峰值 %d 超过预算 %d", st.PeakResident, budget)
	}
	if st.Spills == 0 {
		t.Error("5 万分组预算 500 应发生溢出")
	}
	if st.RowsWritten != st.RowsRead {
		t.Errorf("写出行数 %d != 读回行数 %d", st.RowsWritten, st.RowsRead)
	}
	if st.RowOps > 2*n {
		t.Errorf("总行处理次数 %d 超过 2*N=%d", st.RowOps, 2*n)
	}
	if err := verify.CompareGroups(groups, verify.InMemory(distinct(n))); err != nil {
		t.Error(err)
	}
}

// TestMultiSpillBitIdentical 同一分区被多次溢出时，结果与全内存聚合逐位相同。
func TestMultiSpillBitIdentical(t *testing.T) {
	threeSpillRows := append(distinct(10),
		row.Row{Key: "k7", Val: 100}, row.Row{Key: "k8", Val: 101}, row.Row{Key: "k9", Val: 102})
	cases := []struct {
		name       string
		numParts   int
		budget     int
		rows       []row.Row
		wantSpills []int // 各分区期望溢出次数，nil 表示不检查
	}{
		{"单分区溢出3次", 1, 3, threeSpillRows, []int{3}},
		{"重复键跨多个片段", 1, 2, append(repeat("a", 20), repeat("b", 20)...), nil},
		{"交错重复键", 2, 2, interleave("a", "b", 40), nil},
		{"多分区多次溢出", 4, 8, repeatKeys(300, 2000), nil},
	}
	for _, c := range cases {
		groups, st := runAgg(t, c.numParts, c.budget, c.rows)
		if err := verify.CompareGroups(groups, verify.InMemory(c.rows)); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.wantSpills != nil {
			for p, want := range c.wantSpills {
				if st.SpillsByPart[p] != want {
					t.Errorf("%s: 分区%d溢出%d次, 期望%d", c.name, p, st.SpillsByPart[p], want)
				}
			}
		}
	}
}

func interleave(a, b string, n int) []row.Row {
	rows := make([]row.Row, n)
	for i := range rows {
		key := a
		if i%2 == 1 {
			key = b
		}
		rows[i] = row.Row{Key: key, Val: float64(i)}
	}
	return rows
}

func repeatKeys(keys, n int) []row.Row {
	rows := make([]row.Row, n)
	for i := range rows {
		rows[i] = row.Row{Key: fmt.Sprintf("k%d", i%keys), Val: float64(i)}
	}
	return rows
}
