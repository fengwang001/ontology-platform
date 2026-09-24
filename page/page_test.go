package page

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/cursor"
	"ontology/row"
)

func ids(rows []row.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func build(keys []float64) (*Store, *Pager) {
	s := &Store{}
	for i, k := range keys {
		s.Insert(row.Row{Key: k, ID: fmt.Sprintf("r%02d", i)})
	}
	return s, New(s)
}

// 十行中六行排序值相同，每页三行翻完：十行恰好各出现一次且顺序同全量排序。
func TestForwardExactlyOnce(t *testing.T) {
	cases := []struct {
		name string
		keys []float64
		size int
	}{
		{"six-dup-keys", []float64{1, 2, 5, 5, 5, 5, 5, 5, 6, 7}, 3},
		{"all-same-key", []float64{9, 9, 9, 9, 9}, 2},
		{"no-dup", []float64{1, 2, 3, 4, 5, 6, 7}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, pg := build(tc.keys)
			var got []string
			seen := map[string]int{}
			for cur := []byte(nil); ; {
				p, err := pg.Next(cur, tc.size)
				if err != nil || len(p.Rows) == 0 {
					break
				}
				for _, r := range p.Rows {
					got = append(got, r.ID)
					seen[r.ID]++
				}
				cur = p.Next
			}
			if !slices.Equal(got, ids(s.All())) {
				t.Fatalf("order: got %v, want %v", got, ids(s.All()))
			}
			for id, n := range seen {
				if n != 1 {
					t.Fatalf("row %s appeared %d times", id, n)
				}
			}
		})
	}
}

// 正向翻到第三页后用其反向游标翻一页，必须逐行等于第二页（非倒序）。
func TestBackwardMatchesForward(t *testing.T) {
	_, pg := build([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	p1, _ := pg.Next(nil, 3)
	p2, _ := pg.Next(p1.Next, 3)
	p3, _ := pg.Next(p2.Next, 3)
	back, err := pg.Prev(p3.Prev, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(back.Rows), ids(p2.Rows)) {
		t.Fatalf("backward page %v != forward page2 %v", ids(back.Rows), ids(p2.Rows))
	}
}

// 翻页中增删：已返回行不重复、新区间插入行按序出现、被删行不出现。
func TestConcurrentMutationSemantics(t *testing.T) {
	s, pg := build([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	p1, _ := pg.Next(nil, 3)
	p2, _ := pg.Next(p1.Next, 3)
	s.Insert(row.Row{Key: 0.5, ID: "x1"})  // 已翻过区间
	s.Insert(row.Row{Key: 2.5, ID: "x2"})  // 已翻过区间
	s.Insert(row.Row{Key: 9.5, ID: "y1"})  // 第四页区间
	s.Insert(row.Row{Key: 10.5, ID: "y2"}) // 第四页区间
	s.Delete("r07")                        // 删除第三页的一行（Key=7）
	p3, _ := pg.Next(p2.Next, 3)
	p4, _ := pg.Next(p3.Next, 3)
	var got []string
	for _, p := range []Page{p1, p2, p3, p4} {
		got = append(got, ids(p.Rows)...)
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
		if seen[id] > 1 {
			t.Fatalf("duplicate row %s in %v", id, got)
		}
	}
	if slices.Contains(got, "r07") {
		t.Fatalf("deleted row r07 appeared in %v", got)
	}
	i1, i2 := slices.Index(got, "y1"), slices.Index(got, "y2")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("inserted rows y1,y2 must appear in key order: %v", got)
	}
}

// 单次翻页比较行数不超过 4*(n + ceil(log2 N))。
func TestCompareBound(t *testing.T) {
	const n, size = 20, 10000
	s := &Store{}
	for i := 0; i < size; i++ {
		s.Insert(row.Row{Key: float64(i), ID: fmt.Sprintf("r%05d", i)})
	}
	pg := New(s)
	mid, _ := pg.Next(nil, size/2)
	fwd, err := pg.Next(mid.Next, n)
	if err != nil {
		t.Fatal(err)
	}
	back, err := pg.Prev(mid.Prev, n)
	if err != nil {
		t.Fatal(err)
	}
	bound := CompareBound(n, size)
	if fwd.Compares() > bound || back.Compares() > bound {
		t.Fatalf("compares fwd=%d back=%d exceed bound %d", fwd.Compares(), back.Compares(), bound)
	}
}

// 同一游标多协程并发翻页结果逐行相同；增删并发进行，race 干净。
func TestConcurrentPagingPure(t *testing.T) {
	s := &Store{}
	for i := 0; i < 1000; i++ {
		s.Insert(row.Row{Key: float64(i), ID: fmt.Sprintf("r%04d", i)})
	}
	pg := New(s)
	anchor, _ := pg.Next(nil, 500)
	const workers = 8
	pages := make([][]string, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				p, err := pg.Next(anchor.Next, 20)
				if err != nil {
					t.Error(err)
					return
				}
				pages[w] = ids(p.Rows)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.Insert(row.Row{Key: 2000 + float64(i), ID: fmt.Sprintf("n%04d", i)})
			s.Delete(fmt.Sprintf("n%04d", i))
		}
	}()
	wg.Wait()
	for w := 1; w < workers; w++ {
		if !slices.Equal(pages[0], pages[w]) {
			t.Fatalf("worker %d page differs: %v vs %v", w, pages[w], pages[0])
		}
	}
}

// 空游标从头开始；指向已删除行的游标仍可续翻；跨方向复用被拒。
func TestCursorEdgeCases(t *testing.T) {
	s, pg := build([]float64{1, 2, 3, 4, 5, 6})
	first, err := pg.Next(nil, 2)
	if err != nil || !slices.Equal(ids(first.Rows), []string{"r00", "r01"}) {
		t.Fatalf("empty cursor must start from head: %v %v", ids(first.Rows), err)
	}
	s.Delete("r01") // 删除 first.Next 指向的行
	cont, err := pg.Next(first.Next, 2)
	if err != nil || !slices.Equal(ids(cont.Rows), []string{"r02", "r03"}) {
		t.Fatalf("cursor to deleted row must still page: %v %v", ids(cont.Rows), err)
	}
	if _, err := pg.Prev(first.Next, 2); !errors.Is(err, cursor.ErrCrossDir) {
		t.Fatalf("forward cursor used backward: got %v, want ErrCrossDir", err)
	}
	if _, err := pg.Next(first.Prev, 2); !errors.Is(err, cursor.ErrCrossDir) {
		t.Fatalf("backward cursor used forward: got %v, want ErrCrossDir", err)
	}
}
