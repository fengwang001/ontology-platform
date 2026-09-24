// Command demo 演示游标分页器的关键性质，逐条打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/audit"
	"ontology/cursor"
	"ontology/page"
	"ontology/row"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK   " + name)
	} else {
		failed++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	check("六行同排序值时十行各出现一次", checkDupKeys())
	check("反向翻页逐行等于正向第二页", checkBackward())
	check("并发增删下不重不漏", checkConcurrentMutation())
	check("单次翻页比较行数在上界内", checkCompareBound())
	check("游标逐比特翻转全部被拒且分类正确", checkBitFlip())
	check("跨方向复用游标被拒", checkCrossDir())
	check("空游标从头开始", checkEmptyCursor())
	check("指向已删除行的游标仍可续翻", checkDeletedRowCursor())
	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func idsOf(rows []row.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func newStore(keys ...float64) (*page.Store, *page.Pager) {
	s := &page.Store{}
	for i, k := range keys {
		s.Insert(row.Row{Key: k, ID: fmt.Sprintf("r%02d", i)})
	}
	return s, page.New(s)
}

// 十行中六行排序值相同，每页三行翻完，十行恰好各出现一次。
func checkDupKeys() bool {
	s, pg := newStore(1, 2, 5, 5, 5, 5, 5, 5, 6, 7)
	var pages [][]row.Row
	for cur := []byte(nil); ; {
		p, err := pg.Next(cur, 3)
		if err != nil || len(p.Rows) == 0 {
			break
		}
		pages = append(pages, p.Rows)
		cur = p.Next
	}
	return audit.ExactlyOnce(pages, s.All()) == nil
}

// 正向翻到第三页，用该页反向游标翻一页，须逐行等于第二页。
func checkBackward() bool {
	_, pg := newStore(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	p1, _ := pg.Next(nil, 3)
	p2, _ := pg.Next(p1.Next, 3)
	p3, _ := pg.Next(p2.Next, 3)
	back, err := pg.Prev(p3.Prev, 3)
	return err == nil && slices.Equal(idsOf(back.Rows), idsOf(p2.Rows))
}

// 翻到第二页后增删：已返回行无重复、新区间插入行按序出现、被删行不出现。
func checkConcurrentMutation() bool {
	s, pg := newStore(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	p1, _ := pg.Next(nil, 3)
	p2, _ := pg.Next(p1.Next, 3)
	s.Insert(row.Row{Key: 0.5, ID: "x1"})
	s.Insert(row.Row{Key: 2.5, ID: "x2"})
	s.Insert(row.Row{Key: 9.5, ID: "y1"})
	s.Insert(row.Row{Key: 10.5, ID: "y2"})
	s.Delete("r06")
	p3, _ := pg.Next(p2.Next, 3)
	p4, _ := pg.Next(p3.Next, 3)
	pages := [][]row.Row{p1.Rows, p2.Rows, p3.Rows, p4.Rows}
	if audit.NoRepeats(pages) != nil {
		return false
	}
	var got []string
	for _, p := range pages {
		got = append(got, idsOf(p)...)
	}
	i1, i2 := slices.Index(got, "y1"), slices.Index(got, "y2")
	return !slices.Contains(got, "r06") && i1 >= 0 && i2 >= 0 && i1 < i2
}

// 一万行每页二十行，单次翻页比较次数不超过 4*(20+ceil(log2 10000))。
func checkCompareBound() bool {
	s := &page.Store{}
	for i := 0; i < 10000; i++ {
		s.Insert(row.Row{Key: float64(i), ID: fmt.Sprintf("r%05d", i)})
	}
	pg := page.New(s)
	mid, _ := pg.Next(nil, 5000)
	p, err := pg.Next(mid.Next, 20)
	return err == nil && p.Compares() <= page.CompareBound(20, 10000)
}

// 正向游标用于反向翻页必须报 ErrCrossDir。
func checkCrossDir() bool {
	_, pg := newStore(1, 2, 3, 4)
	p, _ := pg.Next(nil, 2)
	_, err := pg.Prev(p.Next, 2)
	return errors.Is(err, cursor.ErrCrossDir)
}

// 空游标表示从头开始。
func checkEmptyCursor() bool {
	_, pg := newStore(1, 2, 3, 4)
	p, err := pg.Next(nil, 2)
	return err == nil && slices.Equal(idsOf(p.Rows), []string{"r00", "r01"})
}

// 游标指向的行被删除后，仍可按复合键续翻。
func checkDeletedRowCursor() bool {
	s, pg := newStore(1, 2, 3, 4)
	p, _ := pg.Next(nil, 2)
	s.Delete("r01")
	cont, err := pg.Next(p.Next, 2)
	return err == nil && slices.Equal(idsOf(cont.Rows), []string{"r02", "r03"})
}

// 对合法游标逐比特翻转，所有变体必须被拒且落入三类错误之一。
func checkBitFlip() bool {
	raw := cursor.Encode(cursor.After(row.Row{Key: 2.5, ID: "row-0042"}, cursor.Forward))
	for i := range raw {
		for bit := 0; bit < 8; bit++ {
			bad := append([]byte(nil), raw...)
			bad[i] ^= 1 << bit
			_, err := cursor.Decode(bad)
			if err == nil {
				return false
			}
			if !errors.Is(err, cursor.ErrChecksum) &&
				!errors.Is(err, cursor.ErrTruncated) &&
				!errors.Is(err, cursor.ErrDirection) {
				return false
			}
		}
	}
	return true
}
