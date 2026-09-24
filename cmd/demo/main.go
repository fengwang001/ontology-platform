package main

import (
	"errors"
	"fmt"

	"ontology/audit"
	"ontology/cursor"
	"ontology/page"
	"ontology/row"
)

var tieRows = []row.Row{
	row.New(0, "z0"), row.New(1, "a"), row.New(1, "b"), row.New(1, "c"), row.New(1, "d"),
	row.New(1, "e"), row.New(1, "f"), row.New(2, "q"), row.New(2, "r"), row.New(3, "x"),
}

// 逐比特翻转合法游标，任何变体都必须被拒绝且可分类。
func checkBitFlips() bool {
	stats := map[error]int{}
	for _, d := range []cursor.Direction{cursor.Forward, cursor.Backward} {
		raw := cursor.Encode(row.New(1.25, "id-ab"), d)
		for i := range raw {
			for bit := 0; bit < 8; bit++ {
				v := append([]byte(nil), raw...)
				v[i] ^= 1 << bit
				_, _, err := cursor.Decode(v, d)
				switch {
				case err == nil:
					return false
				case errors.Is(err, cursor.ErrChecksum):
					stats[cursor.ErrChecksum]++
				case errors.Is(err, cursor.ErrCursorMalformed):
					stats[cursor.ErrCursorMalformed]++
				case errors.Is(err, cursor.ErrBadDirection):
					stats[cursor.ErrBadDirection]++
				default:
					return false
				}
			}
		}
	}
	return stats[cursor.ErrChecksum] == 272 &&
		stats[cursor.ErrCursorMalformed] == 48 &&
		stats[cursor.ErrBadDirection] == 16
}

// 六行同 Score：十行恰好各出现一次。
func checkTiePagination() bool {
	s := page.NewStore(tieRows)
	want := []string{"z0", "a", "b", "c", "d", "e", "f", "q", "r", "x"}
	seen := map[string]bool{}
	n := 0
	cur := []byte(nil)
	for {
		res, err := s.Forward(cur, 3)
		if err != nil {
			return false
		}
		for _, r := range res.Rows() {
			if n >= len(want) || seen[r.ID] || r.ID != want[n] {
				return false
			}
			seen[r.ID], n = true, n+1
		}
		if len(res.Next()) == 0 {
			return n == len(want)
		}
		cur = res.Next()
	}
}

// 正向翻到第三页，用 Prev 反向翻一页得到第二页。
func checkReversePage() bool {
	s := page.NewStore(tieRows)
	cur, varPrev := []byte(nil), []byte(nil)
	for p := 1; p <= 3; p++ {
		res, err := s.Forward(cur, 3)
		if err != nil {
			return false
		}
		if p == 3 {
			varPrev = res.Prev()
		}
		cur = res.Next()
	}
	res, err := s.Backward(varPrev, 3)
	if err != nil || len(res.Rows()) != 3 {
		return false
	}
	for i, want := range []string{"c", "d", "e"} {
		if res.Rows()[i].ID != want {
			return false
		}
	}
	return true
}

// 一万行每页二十行，比较次数不超 4*(20+ceil(log2(10000)))。
func checkCompareBound() bool {
	var seed []row.Row
	for i := 0; i < 10000; i++ {
		seed = append(seed, row.New(float64(i), fmt.Sprintf("id-%05d", i)))
	}
	s := page.NewStore(seed)
	for pageNo, cur := 0, []byte(nil); pageNo < 10; pageNo++ {
		res, err := s.Forward(cur, 20)
		if err != nil || res.Compared() > audit.CompareBound(10000, 20) {
			return false
		}
		cur = res.Next()
	}
	return true
}

// 游标指向的行被删除后仍以复合键位置续翻。
func checkDeletedCursor() bool {
	s := page.NewStore(tieRows)
	res, _ := s.Forward(nil, 3)
	s.Delete("a")
	s.Delete("b")
	s.Delete("c")
	res, err := s.Forward(res.Next(), 3)
	if err != nil || len(res.Rows()) != 3 {
		return false
	}
	for i, want := range []string{"d", "e", "f"} {
		if res.Rows()[i].ID != want {
			return false
		}
	}
	return true
}

// 翻到第二页后插入/删除：不重、不漏、新行有序。
func checkChurn() bool {
	seed := []row.Row{row.New(0, "p0")}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		seed = append(seed, row.New(1, id))
	}
	for _, id := range []string{"u", "v", "w", "x"} {
		seed = append(seed, row.New(2, id))
	}
	s := page.NewStore(seed)
	rep := audit.RunChurn(s, 3, func() {
		s.Insert(row.New(0, "old1"))
		s.Insert(row.New(0, "old2"))
		s.Insert(row.New(4, "n1"))
		s.Insert(row.New(4, "n2"))
		s.Delete("f")
	})
	if rep.Dupe {
		return false
	}
	ids := map[string]bool{}
	for _, r := range rep.Seen {
		ids[r.ID] = true
	}
	if ids["f"] || !ids["n1"] || !ids["n2"] {
		return false
	}
	full := audit.FullScan(s, 3)
	return len(full) == s.Len() && audit.VerifyUniqueAndOrdered(full) == nil
}

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"六行同值十行各出现一次", checkTiePagination()},
		{"反向页逐行等于正向第二页", checkReversePage()},
		{"并发增删不重不漏新行有序", checkChurn()},
		{"单次翻页比较数不超上界", checkCompareBound()},
		{"逐比特翻转全拒且分类正确", checkBitFlips()},
		{"跨方向复用游标被拒", func() bool {
			raw := cursor.Encode(row.New(1, "x"), cursor.Forward)
			_, _, err := cursor.Decode(raw, cursor.Backward)
			return errors.Is(err, cursor.ErrWrongDirection)
		}()},
		{"空游标表示从头开始", func() bool {
			_, atStart, err := cursor.Decode(nil, cursor.Forward)
			return atStart && err == nil
		}()},
		{"游标指向已删除行仍续翻", checkDeletedCursor()},
	}
	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status, pass = "OK", pass+1
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("总计 %d/%d 通过\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo 判定失败")
	}
}
