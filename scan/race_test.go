package scan

import (
	"math/rand"
	"sync"
	"testing"

	"ontology/zone"
)

// 多 goroutine 并发扫描同一个段（共享扫描器），
// 各自游标互不干扰，结果与串行一致。go test -race 必须干净。
func TestConcurrentScans(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	const n = 4000
	vals := make([]int64, n)
	nulls := make([]bool, n)
	for i := range vals {
		if rng.Intn(6) == 0 {
			nulls[i] = true
			continue
		}
		vals[i] = int64(rng.Intn(500) - 250)
	}
	s := buildSeg(t, 32, vals, nulls)
	pred := zone.And(zone.Ge(-100), zone.Le(100))
	sc := New(s, pred)
	want, err := sc.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	results := make([][]Row, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// 每个 goroutine 用自己的游标分批扫完。
			var got []Row
			cur := Cursor{}
			for !sc.Done(cur) {
				rows, next, err := sc.ScanFrom(cur, 7+w)
				if err != nil {
					t.Error(err)
					return
				}
				got = append(got, rows...)
				cur = next
			}
			results[w] = got
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		if len(results[w]) != len(want) {
			t.Fatalf("worker %d: got %d rows want %d", w, len(results[w]), len(want))
		}
		for i := range want {
			if results[w][i] != want[i] {
				t.Fatalf("worker %d row %d mismatch", w, i)
			}
		}
	}
}

// 只读查询不触发解码：查询前后计数器不变，且连查两次结果相同。
func TestQueriesDoNotDecode(t *testing.T) {
	rng := rand.New(rand.NewSource(37))
	vals := make([]int64, 500)
	for i := range vals {
		vals[i] = int64(rng.Intn(100))
	}
	s := buildSeg(t, 50, vals, nil)
	sc := New(s, zone.And(zone.Eq(42)))
	query := func() (int, int, []string) {
		var encs []string
		for g := 0; g < s.GroupCount(); g++ {
			st, err := s.GroupStats(g)
			if err != nil {
				t.Fatal(err)
			}
			if st.Rows <= 0 {
				t.Fatal("bad stats")
			}
			enc, err := s.GroupEncoding(g)
			if err != nil {
				t.Fatal(err)
			}
			encs = append(encs, enc.String())
		}
		return s.RowCount(), s.GroupCount(), encs
	}
	r1, g1, e1 := query()
	r2, g2, e2 := query()
	if r1 != r2 || g1 != g2 {
		t.Fatal("repeated queries differ")
	}
	for i := range e1 {
		if e1[i] != e2[i] {
			t.Fatal("repeated encoding queries differ")
		}
	}
	if sc.DecodedGroups() != 0 || sc.DecodedValues() != 0 {
		t.Fatalf("queries triggered decoding: groups=%d values=%d",
			sc.DecodedGroups(), sc.DecodedValues())
	}
}
