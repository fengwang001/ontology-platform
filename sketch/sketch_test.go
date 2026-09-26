package sketch

import "testing"

// TestQueryCellAccess proves Query touches exactly d cells no matter how
// many distinct keys were added: it reads the non-exported counter
// directly (white-box, same package — the value never leaves via API).
func TestQueryCellAccess(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		for _, d := range []int{1, 3, 5} {
			s := New(64, d)
			for i := 0; i < m; i++ {
				s.Add(int64(i), 1)
			}
			s.Query(0)
			if s.lastQueryCells != d {
				t.Fatalf("m=%d d=%d: query touched %d cells, want %d",
					m, d, s.lastQueryCells, d)
			}
		}
	}
}

// TestAddAccumulatesEveryRow pins the per-row accumulation of Add.
func TestAddAccumulatesEveryRow(t *testing.T) {
	s := New(6, 3)
	s.Add(2, 4)
	s.Add(5, 2)
	s.Add(11, 1)
	want := [3][6]int64{
		{0, 0, 4, 0, 0, 3}, // row 1: col2=4, col5=2+1
		{0, 0, 0, 0, 0, 7}, // row 2: col5=4+2+1
		{0, 4, 0, 0, 3, 0}, // row 3: col1=4, col4=2+1
	}
	for j := 0; j < 3; j++ {
		for c := 0; c < 6; c++ {
			if s.table[j][c] != want[j][c] {
				t.Fatalf("row %d col %d = %d, want %d", j+1, c, s.table[j][c], want[j][c])
			}
		}
	}
	if q := s.Query(2); q != 4 {
		t.Fatalf("Query(2) = %d, want 4", q)
	}
}
