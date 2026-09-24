package page

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"

	"ontology/cursor"
	"ontology/row"
)

// dupStore 构造 10 行、其中 6 行排序值相同的数据集。
func dupStore() *Store {
	s := NewStore()
	keys := []float64{1, 1, 2, 2, 2, 2, 2, 2, 3, 3}
	for i, k := range keys {
		s.Upsert(row.Row{Key: k, ID: fmt.Sprintf("r%02d", i)})
	}
	return s
}

func ids(rows []row.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// collectForward 从空游标开始逐页翻完，返回各页。
func collectForward(s *Store, n int) [][]row.Row {
	var pages [][]row.Row
	var c cursor.Cursor
	for {
		res, err := Forward(s, c, n)
		if err != nil {
			panic(err)
		}
		if len(res.Rows) == 0 {
			return pages
		}
		pages = append(pages, res.Rows)
		c = res.Next
	}
}

func TestForwardDuplicateKeys(t *testing.T) {
	s := dupStore()
	pages := collectForward(s, 3)
	wantPages := [][]string{
		{"r00", "r01", "r02"}, {"r03", "r04", "r05"},
		{"r06", "r07", "r08"}, {"r09"},
	}
	if len(pages) != len(wantPages) {
		t.Fatalf("页数 = %d, want %d", len(pages), len(wantPages))
	}
	var got []row.Row
	for i, p := range pages {
		if !slices.Equal(ids(p), wantPages[i]) {
			t.Fatalf("第 %d 页 = %v, want %v", i, ids(p), wantPages[i])
		}
		got = append(got, p...)
	}
	// 十行恰好各出现一次，顺序与一次性全量排序一致。
	if !slices.Equal(got, s.Snapshot()) {
		t.Fatalf("翻页拼接 %v != 全量排序 %v", ids(got), ids(s.Snapshot()))
	}
}

func TestBackwardMatchesForward(t *testing.T) {
	s := dupStore()
	var c cursor.Cursor
	var pages []Result
	for i := 0; i < 3; i++ {
		res, err := Forward(s, c, 3)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, res)
		c = res.Next
	}
	back, err := Backward(s, pages[2].Prev, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 反向翻一页得到第二页，且逐行相同（不是倒序）。
	if !slices.Equal(ids(back.Rows), ids(pages[1].Rows)) {
		t.Fatalf("反向页 = %v, want 正向第二页 %v", ids(back.Rows), ids(pages[1].Rows))
	}
}

func TestConcurrentMutations(t *testing.T) {
	s := NewStore()
	for i := 1; i <= 20; i++ {
		s.Upsert(row.Row{Key: float64(i), ID: fmt.Sprintf("k%02d", i)})
	}
	var c cursor.Cursor
	var returned []row.Row
	for i := 0; i < 2; i++ { // 翻到第二页
		res, _ := Forward(s, c, 5)
		returned = append(returned, res.Rows...)
		c = res.Next
	}
	s.Upsert(row.Row{Key: 2.5, ID: "old1"}) // 第一页区间
	s.Upsert(row.Row{Key: 3.5, ID: "old2"})
	s.Upsert(row.Row{Key: 16.5, ID: "new1"}) // 第四页区间
	s.Upsert(row.Row{Key: 17.5, ID: "new2"})
	s.Delete("k12") // 第三页的一行
	for {
		res, _ := Forward(s, c, 5)
		if len(res.Rows) == 0 {
			break
		}
		returned = append(returned, res.Rows...)
		c = res.Next
	}
	seen := map[string]bool{}
	for _, r := range returned {
		if seen[r.ID] {
			t.Fatalf("行 %s 重复出现", r.ID)
		}
		seen[r.ID] = true
	}
	if seen["k12"] {
		t.Fatal("被删除的 k12 不应出现")
	}
	got := ids(returned)
	i1, i2 := slices.Index(got, "new1"), slices.Index(got, "new2")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("新插入行应按复合键顺序出现: %v", got)
	}
	for _, id := range []string{"old1", "old2"} {
		if seen[id] {
			t.Fatalf("插入已翻过区间的 %s 不应出现", id)
		}
	}
}

func TestComparisonBound(t *testing.T) {
	s := NewStore()
	const n = 10000
	for i := 0; i < n; i++ {
		s.Upsert(row.Row{Key: float64(i), ID: fmt.Sprintf("r%05d", i)})
	}
	bound := int64(4 * (20 + int(math.Ceil(math.Log2(n)))))
	c := cursor.Cursor{}
	for page := 0; page < 3; page++ {
		res, err := Forward(s, c, 20)
		if err != nil {
			t.Fatal(err)
		}
		if got := LastComparisons(); got > bound {
			t.Fatalf("第 %d 页比较 %d 次, 超过上界 %d", page, got, bound)
		}
		c = res.Next
	}
}

func TestCrossDirectionReuse(t *testing.T) {
	s := dupStore()
	res, _ := Forward(s, cursor.Cursor{}, 3)
	if _, err := Backward(s, res.Next, 3); !errors.Is(err, cursor.ErrDirectionMismatch) {
		t.Fatalf("正向游标做反向翻页: err = %v", err)
	}
	back, _ := Backward(s, cursor.Cursor{}, 3)
	if _, err := Forward(s, back.Prev, 3); !errors.Is(err, cursor.ErrDirectionMismatch) {
		t.Fatalf("反向游标做正向翻页: err = %v", err)
	}
}

func TestDeletedAnchorCursor(t *testing.T) {
	s := dupStore()
	p1, _ := Forward(s, cursor.Cursor{}, 3)
	s.Delete(p1.Rows[2].ID) // 删除游标锚定的行
	p2, err := Forward(s, p1.Next, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(p2.Rows), []string{"r03", "r04", "r05"}) {
		t.Fatalf("续翻 = %v, want [r03 r04 r05]", ids(p2.Rows))
	}
}

func TestEmptyCursorFromStart(t *testing.T) {
	s := dupStore()
	c, err := cursor.Decode(nil)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := Forward(s, c, 3)
	if !slices.Equal(ids(res.Rows), []string{"r00", "r01", "r02"}) {
		t.Fatalf("空游标应从头开始, got %v", ids(res.Rows))
	}
}

func TestConcurrentSameCursor(t *testing.T) {
	s := dupStore()
	anchor, _ := Forward(s, cursor.Cursor{}, 3)
	want, _ := Forward(s, anchor.Next, 3)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				got, err := Forward(s, anchor.Next, 3)
				if err != nil || !slices.Equal(ids(got.Rows), ids(want.Rows)) {
					t.Error("并发翻页结果不一致")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestRaceReadWrite(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s.Upsert(row.Row{Key: float64(i % 50), ID: fmt.Sprintf("w%d-%d", g, i)})
				s.Delete(fmt.Sprintf("w%d-%d", g, i-1))
			}
		}(g)
		go func() {
			defer wg.Done()
			c := cursor.Cursor{}
			for i := 0; i < 200; i++ {
				res, err := Forward(s, c, 5)
				if err != nil {
					t.Error(err)
					return
				}
				if len(res.Rows) > 0 {
					c = res.Next
				}
			}
		}()
	}
	wg.Wait()
}
