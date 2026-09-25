package ranking

import (
	"math"
	"testing"
)

// NaN 排序值的行被拒绝并计入跳过计数，不参与任何排名。
func TestNaNSkipped(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: math.NaN(), ID: "nan1"},
		{Partition: strPtr("p"), Value: 1, ID: "ok1"},
		{Partition: strPtr("p"), Value: math.NaN(), ID: "nan2"},
		{Partition: strPtr("p"), Value: 2, ID: "ok2"},
	}
	got := Rank(rows, Options{})
	if got.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", got.Skipped)
	}
	assertTriples(t, got, []string{"ok1", "ok2"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
	})
}

// +0.0 与 -0.0 视为相等，构成并列。
func TestSignedZeroTied(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 0.0, ID: "pos"},
		{Partition: strPtr("p"), Value: math.Copysign(0, -1), ID: "neg"},
	}
	got := Rank(rows, Options{})
	assertTriples(t, got, []string{"neg", "pos"}, []triple{
		{1, 1, 1},
		{2, 1, 1},
	})
}

// ±Inf 是合法排序值，参与排名并排在两端。
func TestInfinityRanksAtEnds(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: math.Inf(1), ID: "posinf"},
		{Partition: strPtr("p"), Value: 0, ID: "mid"},
		{Partition: strPtr("p"), Value: math.Inf(-1), ID: "neginf"},
	}
	asc := Rank(rows, Options{})
	assertTriples(t, asc, []string{"neginf", "mid", "posinf"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{3, 3, 3},
	})
	desc := Rank(rows, Options{Descending: true})
	assertTriples(t, desc, []string{"posinf", "mid", "neginf"}, []triple{
		{1, 1, 1},
		{2, 2, 2},
		{3, 3, 3},
	})
}

// nil 分区键与 NaN 同时出现时，跳过计数是两者之和。
func TestSkippedCountAccumulates(t *testing.T) {
	rows := []Row{
		{Partition: nil, Value: 1, ID: "bad1"},
		{Partition: strPtr("p"), Value: math.NaN(), ID: "bad2"},
		{Partition: nil, Value: math.NaN(), ID: "bad3"},
		{Partition: strPtr("p"), Value: 1, ID: "ok"},
	}
	got := Rank(rows, Options{})
	if got.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3", got.Skipped)
	}
	if len(got.Rows) != 1 || got.Rows[0].Row.ID != "ok" {
		t.Errorf("Rows = %+v, want single row ok", got.Rows)
	}
}
