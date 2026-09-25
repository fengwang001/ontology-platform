package ontology

import (
	"math/rand"
	"reflect"
	"testing"
)

func mkRows(values []float64, idPrefix string) []Row {
	key := "p"
	rows := make([]Row, len(values))
	for i, v := range values {
		rows[i] = Row{Partition: &key, Value: v, ID: idPrefix + itoa(i+1)}
	}
	return rows
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func assertTriples(t *testing.T, rows []Row, want [][3]int) {
	t.Helper()
	got := Rank(rows, Asc).Rows
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].RowNumber != want[i][0] || got[i].Rank != want[i][1] || got[i].DenseRank != want[i][2] {
			t.Fatalf("row %d (%s) triple = (%d,%d,%d), want (%d,%d,%d)",
				i, got[i].ID, got[i].RowNumber, got[i].Rank, got[i].DenseRank,
				want[i][0], want[i][1], want[i][2])
		}
	}
}

func TestThreeColumns_10_20_20_30(t *testing.T) {
	assertTriples(t, mkRows([]float64{10, 20, 20, 30}, "r"), [][3]int{
		{1, 1, 1}, {2, 2, 2}, {3, 2, 2}, {4, 4, 3},
	})
}

func TestThreeColumns_AllTies(t *testing.T) {
	assertTriples(t, mkRows([]float64{5, 5, 5, 5}, "r"), [][3]int{
		{1, 1, 1}, {2, 1, 1}, {3, 1, 1}, {4, 1, 1},
	})
}

func TestThreeColumns_MiddleTie(t *testing.T) {
	assertTriples(t, mkRows([]float64{1, 2, 2, 2, 3}, "r"), [][3]int{
		{1, 1, 1}, {2, 2, 2}, {3, 2, 2}, {4, 2, 2}, {5, 5, 3},
	})
}

func TestPermutationsProduceIdenticalOutput(t *testing.T) {
	base := mkRows([]float64{10, 20, 20, 30, 20, 10, 5, 5}, "id-")
	want := Rank(base, Asc).Rows
	rng := rand.New(rand.NewSource(42))
	for p := 0; p < 20; p++ {
		shuffled := append([]Row(nil), base...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := Rank(shuffled, Asc).Rows
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d:\n got %#v\nwant %#v", p, got, want)
		}
	}
}
