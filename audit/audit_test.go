package audit

import (
	"fmt"
	"sync"
	"testing"

	"ontology/page"
	"ontology/row"
)

func churnSeed() []row.Row {
	rows := []row.Row{row.New(0, "p0")}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		rows = append(rows, row.New(1, id))
	}
	for i := 2; i <= 5; i++ {
		rows = append(rows, row.New(float64(i), "q"))
		rows[len(rows)-1].ID = string(rune('q' + i)) // u,v,w,x 唯一 ID
	}
	return rows
}

func TestFullScanNoDupNoSkip(t *testing.T) {
	cases := []struct {
		name string
		size int
	}{
		{"page 1", 1},
		{"page 3", 3},
		{"page 7", 7},
		{"page larger than n", 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := page.NewStore(churnSeed())
			got := FullScan(s, tc.size)
			if len(got) != s.Len() {
				t.Fatalf("遍历 %d 行, 数据集 %d 行", len(got), s.Len())
			}
			if err := VerifyUniqueAndOrdered(got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 翻到第二页后：往第一页区间插两行、第四页区间插两行、删第三页一行。
func TestChurnInsertDelete(t *testing.T) {
	s := page.NewStore(churnSeed())
	insertLater := []row.Row{row.New(4, "n1"), row.New(4, "n2")} // 第四页区间
	rep := RunChurn(s, 3, func() {
		s.Insert(row.New(0, "old1")) // 第一页区间（已翻过）
		s.Insert(row.New(0, "old2"))
		for _, r := range insertLater {
			s.Insert(r)
		}
		s.Delete("f") // 第三页首行 (1,f)
	})
	if rep.Dupe {
		t.Fatal("已返回行重复出现")
	}
	ids := map[string]int{}
	for i, r := range rep.Seen {
		ids[r.ID] = i
	}
	if _, ok := ids["f"]; ok {
		t.Fatal("被删除的行 f 仍然出现")
	}
	for _, r := range insertLater {
		pos, ok := ids[r.ID]
		if !ok {
			t.Fatalf("第四页区间新插入行 %s 未出现", r.ID)
		}
		if pos > 0 {
			prev := rep.Seen[pos-1]
			if row.Compare(prev, r) > 0 {
				t.Fatalf("新行 %s 未按复合键顺序出现", r.ID)
			}
		}
	}
}

func TestConcurrentPagingAndMutation(t *testing.T) {
	s := page.NewStore(churnSeed())
	base, _ := s.Forward(nil, 3)
	cur := base.Next()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var all []row.Row
			c := append([]byte(nil), cur...)
			for len(c) > 0 {
				res, err := s.Forward(c, 3)
				if err != nil {
					t.Error(err)
					return
				}
				if err := VerifyUniqueAndOrdered(append(all, res.Rows()...)); err != nil {
					t.Error(err)
					return
				}
				all = append(all, res.Rows()...)
				c = res.Next()
			}
		}()
	}
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := row.New(float64(i%4)+10, fmt.Sprintf("tmp-%d", i))
			s.Insert(id)
			s.Delete(id.ID)
		}(i)
	}
	wg.Wait()
}

func TestCompareBound(t *testing.T) {
	cases := []struct {
		n, size int
	}{
		{10000, 20},
		{10, 3},
		{1, 1},
	}
	for _, tc := range cases {
		b := CompareBound(tc.n, tc.size)
		if b <= tc.size {
			t.Fatalf("n=%d size=%d bound=%d 不合理", tc.n, tc.size, b)
		}
	}
}
