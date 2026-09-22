package ontology

import (
	"math"
	"reflect"
	"testing"
)

func TestPartitionIsolationAndOrder(t *testing.T) {
	b, a := "b", "a"
	empty := ""
	rows := []Row{
		{Partition: &b, SortValue: 100, ID: "b1"},
		{Partition: &a, SortValue: 10, ID: "a1"},
		{Partition: &a, SortValue: 5, ID: "a0"},
		{Partition: &empty, SortValue: 7, ID: "e1"},
	}
	res := Rank(rows, Asc)
	gotKeys := []string{}
	firstRank := map[string]int{}
	for _, r := range res.Rows {
		if _, ok := firstRank[r.Partition]; !ok {
			gotKeys = append(gotKeys, r.Partition)
			firstRank[r.Partition] = r.RowNumber
		}
	}
	wantKeys := []string{"", "a", "b"}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("partition order = %v, want %v", gotKeys, wantKeys)
	}
	for _, k := range wantKeys {
		if firstRank[k] != 1 {
			t.Errorf("partition %q first row number = %d, want 1", k, firstRank[k])
		}
	}
	// Within "a", value 5 precedes value 10.
	if res.Rows[1].ID != "a0" || res.Rows[2].ID != "a1" {
		t.Errorf("partition a order = %s,%s, want a0,a1", res.Rows[1].ID, res.Rows[2].ID)
	}
}

func TestSkipCounters(t *testing.T) {
	p := "p"
	rows := []Row{
		{Partition: nil, SortValue: 1, ID: "nil1"},
		{Partition: &p, SortValue: math.NaN(), ID: "nan1"},
		{Partition: nil, SortValue: math.NaN(), ID: "nilnan"},
		{Partition: &p, SortValue: 2, ID: "ok1"},
		{Partition: &p, SortValue: math.NaN(), ID: "nan2"},
	}
	res := Rank(rows, Asc)
	if res.Stats.SkippedNilPartition != 2 {
		t.Errorf("SkippedNilPartition = %d, want 2", res.Stats.SkippedNilPartition)
	}
	if res.Stats.SkippedNaN != 2 {
		t.Errorf("SkippedNaN = %d, want 2", res.Stats.SkippedNaN)
	}
	if len(res.Rows) != 1 || res.Rows[0].ID != "ok1" {
		t.Fatalf("accepted rows = %+v, want only ok1", res.Rows)
	}
}

func TestSignedZeroTiesAndInfinity(t *testing.T) {
	p := "p"
	rows := []Row{
		{Partition: &p, SortValue: math.Inf(-1), ID: "neg-inf"},
		{Partition: &p, SortValue: 0, ID: "plus-zero"},
		{Partition: &p, SortValue: math.Copysign(0, -1), ID: "minus-zero"},
		{Partition: &p, SortValue: math.Inf(1), ID: "pos-inf"},
	}
	res := Rank(rows, Asc)
	ids := []string{}
	for _, r := range res.Rows {
		ids = append(ids, r.ID)
	}
	wantIDs := []string{"neg-inf", "minus-zero", "plus-zero", "pos-inf"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("order = %v, want %v", ids, wantIDs)
	}
	z := res.Rows[1]
	if z.Rank != 2 || z.DenseRank != 2 {
		t.Errorf("first zero rank=%d dense=%d, want 2,2", z.Rank, z.DenseRank)
	}
	if res.Rows[2].Rank != 2 || res.Rows[2].DenseRank != 2 {
		t.Errorf("second zero must tie: %+v", res.Rows[2])
	}
	if res.Rows[3].Rank != 4 || res.Rows[3].DenseRank != 3 {
		t.Errorf("+Inf rank=%d dense=%d, want 4,3", res.Rows[3].Rank, res.Rows[3].DenseRank)
	}
}

func TestInputNotModified(t *testing.T) {
	a, b := "a", "b"
	rows := []Row{
		{Partition: &b, SortValue: 20, ID: "z"},
		{Partition: &a, SortValue: 10, ID: "y"},
		{Partition: &a, SortValue: 10, ID: "x"},
	}
	snapshot := make([]Row, len(rows))
	ptrs := make([]*string, len(rows))
	for i := range rows {
		snapshot[i] = rows[i]
		ptrs[i] = rows[i].Partition
	}
	res := Rank(rows, Desc)
	if !reflect.DeepEqual(rows, snapshot) {
		t.Fatalf("input changed:\n got  %+v\n want %+v", rows, snapshot)
	}
	for i := range rows {
		if rows[i].Partition != ptrs[i] {
			t.Errorf("row %d partition pointer changed", i)
		}
	}
	// Result slice is distinct storage from the input slice.
	res.Rows[0].ID = "mutated"
	if rows[0].ID == "mutated" {
		t.Fatal("result aliases input storage")
	}
}

func TestComparisonCountBound(t *testing.T) {
	for _, n := range []int{2, 8, 64, 1000} {
		rows := make([]Row, n)
		p := "p"
		// Distinct values via index so every comparator decision is real.
		for i := range rows {
			rows[i] = Row{Partition: &p, SortValue: float64(i * 7 % 13), ID: ""}
		}
		// Unique IDs keep the tie-break path total-ordered.
		for i := range rows {
			rows[i].ID = "id" + itoa(i)
		}
		c := &comparisonCounter{}
		rankPartition(rows, Asc, c)
		bound := 10 * n * ceilLog2(n+1)
		if c.Comparisons() > bound {
			t.Errorf("n=%d comparisons=%d > bound=%d (O(n^2)?)",
				n, c.Comparisons(), bound)
		}
		if c.Comparisons() == 0 && n > 1 {
			t.Errorf("n=%d comparator never invoked", n)
		}
	}
}

// ceilLog2 returns ceil(log2(x)) for x >= 1.
func ceilLog2(x int) int {
	k := 0
	for (1 << k) < x {
		k++
	}
	return k
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
