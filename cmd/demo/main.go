package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology"
)

var fails int

func verdict(ok bool, format string, args ...any) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func keyList(p ontology.Page) []string {
	out := make([]string, len(p.Objects))
	for i, o := range p.Objects {
		out[i] = o.Key
	}
	return out
}

// scanAll 在无变更的集合上翻完若干页，校验不重不漏。
func scanAll(s *ontology.Store, limit int) ([]string, ontology.Page, bool) {
	cursor := ""
	var all []string
	var last ontology.Page
	for {
		p, err := s.Scan(cursor, limit)
		if err != nil {
			return nil, last, false
		}
		all = append(all, keyList(p)...)
		last = p
		if !p.HasMore {
			return all, last, true
		}
		cursor = p.Cursor
	}
}

func fill(s *ontology.Store, n int) {
	for i := 0; i < n; i++ {
		s.Put(fmt.Sprintf("k%02d", i), i)
	}
}

func main() {
	// 1) 正常翻完三页，元素不重不漏。
	s := ontology.NewStore()
	fill(s, 9)
	var pages [][]string
	cursor := ""
	var first ontology.Page
	for i := 0; i < 3; i++ {
		p, err := s.Scan(cursor, 3)
		if err != nil {
			verdict(false, "正常翻页第%d页出错: %v", i+1, err)
			return
		}
		pages = append(pages, keyList(p))
		if i == 0 {
			first = p
		}
		cursor = p.Cursor
	}
	merged := append(append(append([]string{}, pages[0]...), pages[1]...), pages[2]...)
	want := []string{"k00", "k01", "k02", "k03", "k04", "k05", "k06", "k07", "k08"}
	verdict(reflect.DeepEqual(merged, want) && cursor == "",
		"正常三页不重不漏: %v", merged)

	// 2) 翻页中途插入靠前元素 + 删除靠后元素：标记与跳过统计。
	s2 := ontology.NewStore()
	fill(s2, 9)
	p1, _ := s2.Scan("", 3)
	s2.Put("k02x", 99) // 插入在续点之前
	s2.Delete("k07")   // 删除尚未翻到的元素
	p2, _ := s2.Scan(p1.Cursor, 3)
	p3, _ := s2.Scan(p2.Cursor, 3)
	skip := s2.Skipped(p1.SessionID())
	gotMerge := append(append(keyList(p1), keyList(p2)...), keyList(p3)...)
	verdict(p2.Changes == ontology.ChangeBoth && p3.Discarded == 1 &&
		skip.Total == 1 && skip.Reasons.DiscardedByDelete == 1 &&
		!contains(gotMerge, "k02x") && !contains(gotMerge, "k07"),
		"中途插入/删除: changes=%s 本页丢弃=%d 累计跳过=%d",
		p2.Changes, p3.Discarded, skip.Total)

	// 3) limit=0：空页且游标不前进。
	z, err := s.Scan(first.Cursor, 0)
	after, _ := s.Scan(first.Cursor, 3)
	verdict(err == nil && len(z.Objects) == 0 && z.Cursor == "" &&
		reflect.DeepEqual(keyList(after), []string{"k03", "k04", "k05"}),
		"limit=0 返回空页且游标不前进")

	// 4) limit=-1：参数错误，与空页可区分。
	_, errNeg := s.Scan("", -1)
	verdict(errors.Is(errNeg, ontology.ErrInvalidLimit),
		"limit=-1 为参数错误 ErrInvalidLimit")

	// 5) 篡改游标：ErrInvalidCursor。
	tampered := first.Cursor[:len(first.Cursor)-2] + "XX"
	_, errBad := s.Scan(tampered, 3)
	verdict(errors.Is(errBad, ontology.ErrInvalidCursor),
		"篡改游标判定为游标无效")

	// 6) 跨会话（会话已显式失效）：ErrSessionInvalid，类别不同。
	s.InvalidateSession(first.SessionID())
	_, errSess := s.Scan(first.Cursor, 3)
	verdict(errors.Is(errSess, ontology.ErrSessionInvalid) &&
		!errors.Is(errSess, ontology.ErrInvalidCursor),
		"已失效会话判定为会话无效（区别于游标无效）")

	// 7) 同一游标并发 Scan 两次返回一致。
	s3 := ontology.NewStore()
	fill(s3, 8)
	cp, _ := s3.Scan("", 3)
	var ra, rb ontology.Page
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ra, _ = s3.Scan(cp.Cursor, 3) }()
	go func() { defer wg.Done(); rb, _ = s3.Scan(cp.Cursor, 3) }()
	wg.Wait()
	verdict(reflect.DeepEqual(keyList(ra), keyList(rb)) &&
		reflect.DeepEqual(keyList(ra), []string{"k03", "k04", "k05"}),
		"同一游标并发Scan幂等一致: %v == %v", keyList(ra), keyList(rb))

	total := 7
	if fails == 0 {
		fmt.Printf("总计 %d/%d 项全部通过\n", total, total)
	} else {
		fmt.Printf("总计 %d/%d 项通过，%d 项失败\n", total-fails, total, fails)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
