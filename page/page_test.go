package page

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"

	"ontology/cursor"
	"ontology/row"
)

// tieDataset：10 行，其中 6 行 Score 完全相同。
func tieDataset() []row.Row {
	return []row.Row{
		row.New(0, "z0"), row.New(1, "a"), row.New(1, "b"), row.New(1, "c"),
		row.New(1, "d"), row.New(1, "e"), row.New(1, "f"), row.New(2, "q"),
		row.New(2, "r"), row.New(3, "x"),
	}
}

func sortedRows(in []row.Row) []row.Row {
	out := append([]row.Row(nil), in...)
	sort.Slice(out, func(i, j int) bool { return row.Less(out[i], out[j]) })
	return out
}

func TestForwardNoDuplicateNoSkip(t *testing.T) {
	s := NewStore(tieDataset())
	want := sortedRows(tieDataset())
	var got []row.Row
	cur := []byte(nil)
	for {
		res, err := s.Forward(cur, 3)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, res.Rows()...)
		if len(res.Next()) == 0 {
			break
		}
		cur = res.Next()
	}
	if len(got) != 10 {
		t.Fatalf("取到 %d 行, want 10", len(got))
	}
	seen := map[string]bool{}
	for i, r := range got {
		if seen[r.ID] {
			t.Fatalf("行 %s 重复出现", r.ID)
		}
		seen[r.ID] = true
		if r != want[i] {
			t.Fatalf("第 %d 行 = %v, want %v", i, r, want[i])
		}
	}
}

func TestBackwardReproducesPageTwo(t *testing.T) {
	s := NewStore(tieDataset())
	want := sortedRows(tieDataset())[3:6] // 正向第二页
	cur := []byte(nil)
	var third []byte
	for pageNo := 1; pageNo <= 3; pageNo++ {
		res, err := s.Forward(cur, 3)
		if err != nil {
			t.Fatal(err)
		}
		if pageNo == 3 {
			third = append([]byte(nil), res.Prev()...)
		}
		cur = res.Next()
	}
	res, err := s.Backward(third, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows()) != 3 {
		t.Fatalf("反向取到 %d 行", len(res.Rows()))
	}
	for i, r := range res.Rows() {
		if r != want[i] {
			t.Fatalf("位置 %d: %v, want %v（不得是第二页倒序）", i, r, want[i])
		}
	}
}

func TestCompareCountBound(t *testing.T) {
	var seed []row.Row
	for i := 0; i < 10000; i++ {
		seed = append(seed, row.New(float64(i), fmt.Sprintf("id-%05d", i)))
	}
	s := NewStore(seed)
	const n = 20
	bound := 4 * (n + int(math.Ceil(math.Log2(10000))))
	cur := []byte(nil)
	for pageNo := 1; pageNo <= 50; pageNo++ {
		res, err := s.Forward(cur, n)
		if err != nil {
			t.Fatal(err)
		}
		if res.Compared() > bound {
			t.Fatalf("第 %d 页比较 %d 次, 超过上界 %d", pageNo, res.Compared(), bound)
		}
		cur = res.Next()
	}
}

func TestCursorOnDeletedRowContinues(t *testing.T) {
	s := NewStore(tieDataset())
	res, _ := s.Forward(nil, 3)
	afterFirst := append([]byte(nil), res.Next()...)
	if !s.Delete("a") || !s.Delete("b") || !s.Delete("c") {
		t.Fatal("删除第一页失败")
	}
	res, err := s.Forward(afterFirst, 3)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, r := range res.Rows() {
		ids = append(ids, r.ID)
	}
	want := []string{"d", "e", "f"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("删除后续翻 = %v, want %v", ids, want)
		}
	}
}

func TestFetchErrors(t *testing.T) {
	s := NewStore(tieDataset())
	cases := []struct {
		name string
		raw  []byte
		dir  cursor.Direction
		want error
	}{
		{"forward cursor backward", cursor.Encode(row.New(1, "a"), cursor.Forward), cursor.Backward, cursor.ErrWrongDirection},
		{"backward cursor forward", cursor.Encode(row.New(1, "a"), cursor.Backward), cursor.Forward, cursor.ErrWrongDirection},
		{"garbage", []byte{1, 2, 3}, cursor.Forward, cursor.ErrCursorMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.dir == cursor.Forward {
				_, err = s.Forward(tc.raw, 3)
			} else {
				_, err = s.Backward(tc.raw, 3)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
}

// 同一游标被多协程并发使用，各自拿到逐行相同的页；同时有增删发生。
func TestConcurrentSameCursor(t *testing.T) {
	s := NewStore(tieDataset())
	first, _ := s.Forward(nil, 3)
	cur := append([]byte(nil), first.Next()...)

	var wg sync.WaitGroup
	results := make([][]row.Row, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			res, err := s.Forward(cur, 3)
			if err != nil {
				t.Error(err)
				return
			}
			results[g] = res.Rows()
		}(g)
	}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Insert(row.New(float64(i%4)+0.5, fmt.Sprintf("c-%d", i)))
			s.Delete(fmt.Sprintf("c-%d", i))
		}(i)
	}
	wg.Wait()
	for g := 1; g < 16; g++ {
		if len(results[g]) != len(results[0]) {
			t.Fatalf("协程 %d 页数不同", g)
		}
		for i := range results[0] {
			if results[g][i] != results[0][i] {
				t.Fatalf("协程 %d 第 %d 行 = %v, want %v", g, i, results[g][i], results[0][i])
			}
		}
	}
}
