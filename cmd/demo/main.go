// Command demo 逐条演示并判定游标分页器的各项保证。
package main

import (
	"fmt"
	"math"
	"os"
	"slices"

	"ontology/cursor"
	"ontology/page"
	"ontology/row"
)

var failed, total int

func check(name string, ok bool, detail string) {
	total++
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	// 复合键 (Key, ID) 在 Key 重复时仍是严格全序。
	dup := []row.Row{{Key: 2, ID: "f"}, {Key: 2, ID: "a"}, {Key: 1, ID: "z"}, {Key: 2, ID: "c"}}
	slices.SortFunc(dup, row.Compare)
	strict := true
	for i := 1; i < len(dup); i++ {
		strict = strict && row.Less(dup[i-1], dup[i])
	}
	check("composite-key total order", strict, "(Key 重复时按 ID 分先后)")

	// 游标逐比特翻转：全部被拒且分类正确。
	src := cursor.Encode(cursor.Cursor{Dir: cursor.Forward, Key: 2.5, ID: "r0042"})
	counts := map[error]int{}
	accepted := 0
	for i := range src {
		for bit := 0; bit < 8; bit++ {
			v := make([]byte, len(src))
			copy(v, src)
			v[i] ^= 1 << bit
			if _, err := cursor.Decode(v); err == nil {
				accepted++
			} else {
				counts[err]++
			}
		}
	}
	classified := counts[cursor.ErrChecksum] == 152 &&
		counts[cursor.ErrTruncated] == 8 && counts[cursor.ErrBadDirection] == 8
	check("bit-flip all rejected", accepted == 0 && classified,
		fmt.Sprintf("(168 变体: 校验失败 %d, 字段不完整 %d, 方向非法 %d, 误接受 %d)",
			counts[cursor.ErrChecksum], counts[cursor.ErrTruncated],
			counts[cursor.ErrBadDirection], accepted))

	// 空游标合法，解码为零游标（从头开始）。
	zero, err := cursor.Decode(nil)
	check("empty cursor decodes to start", err == nil && zero.IsZero(), "")

	// 六行同排序值：十行各出现一次，顺序与全量排序一致。
	s := page.NewStore()
	keys := []float64{1, 1, 2, 2, 2, 2, 2, 2, 3, 3}
	for i, k := range keys {
		s.Upsert(row.Row{Key: k, ID: fmt.Sprintf("r%02d", i)})
	}
	var got []row.Row
	c := cursor.Cursor{}
	for {
		res, _ := page.Forward(s, c, 3)
		if len(res.Rows) == 0 {
			break
		}
		got = append(got, res.Rows...)
		c = res.Next
	}
	check("dup keys: 10 rows once each", slices.Equal(got, s.Snapshot()),
		fmt.Sprintf("(%d 行, 6 行 Key=2.0)", len(got)))

	// 反向翻页 = 正向第二页，逐行相同。
	var fw []page.Result
	c = cursor.Cursor{}
	for i := 0; i < 3; i++ {
		res, _ := page.Forward(s, c, 3)
		fw = append(fw, res)
		c = res.Next
	}
	back, _ := page.Backward(s, fw[2].Prev, 3)
	check("backward page == forward page 2", slices.Equal(back.Rows, fw[1].Rows), "")

	// 并发增删语义：已返回行无重复、未删行不跳过、删掉的行不出现。
	s2 := page.NewStore()
	for i := 1; i <= 20; i++ {
		s2.Upsert(row.Row{Key: float64(i), ID: fmt.Sprintf("k%02d", i)})
	}
	var returned []row.Row
	c = cursor.Cursor{}
	for i := 0; i < 2; i++ {
		res, _ := page.Forward(s2, c, 5)
		returned = append(returned, res.Rows...)
		c = res.Next
	}
	s2.Upsert(row.Row{Key: 2.5, ID: "old1"})
	s2.Upsert(row.Row{Key: 16.5, ID: "new1"})
	s2.Upsert(row.Row{Key: 17.5, ID: "new2"})
	s2.Delete("k12")
	for {
		res, _ := page.Forward(s2, c, 5)
		if len(res.Rows) == 0 {
			break
		}
		returned = append(returned, res.Rows...)
		c = res.Next
	}
	seen := map[string]bool{}
	dup := false
	for _, r := range returned {
		dup = dup || seen[r.ID]
		seen[r.ID] = true
	}
	check("mutation: no dup, deleted gone", !dup && !seen["k12"] && seen["new1"] && seen["new2"],
		fmt.Sprintf("(%d 行已返回)", len(returned)))

	// 单次翻页比较次数有上界。
	big := page.NewStore()
	for i := 0; i < 10000; i++ {
		big.Upsert(row.Row{Key: float64(i), ID: fmt.Sprintf("r%05d", i)})
	}
	page.Forward(big, cursor.Cursor{}, 20)
	cmpN := page.LastComparisons()
	bound := int64(4 * (20 + int(math.Ceil(math.Log2(10000)))))
	check("comparisons within bound", cmpN <= bound,
		fmt.Sprintf("(%d <= %d)", cmpN, bound))

	// 跨方向复用被拒。
	_, err = page.Backward(s, fw[0].Next, 3)
	check("cross-direction reuse rejected", errors.Is(err, cursor.ErrDirectionMismatch), "")

	// 游标锚定行被删除后仍可续翻。
	s.Delete(fw[0].Rows[2].ID)
	cont, err := page.Forward(s, fw[0].Next, 3)
	check("deleted-anchor cursor continues", err == nil && slices.Equal(cont.Rows, fw[1].Rows), "")

	fmt.Printf("TOTAL %d checks, %d failed\n", total, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
