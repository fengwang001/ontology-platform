package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology"
)

var pass, fail int

func report(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fail++
	} else {
		pass++
	}
	fmt.Printf("%s  %s  %s\n", tag, name, detail)
}

func keys(p ontology.Page) []string {
	out := make([]string, len(p.Items))
	for i, item := range p.Items {
		out[i] = item.Key
	}
	return out
}

func main() {
	// 1) 正常翻完三页，元素不重不漏。
	st := ontology.NewStore()
	for i := 0; i < 7; i++ {
		st.Put(fmt.Sprintf("k%02d", i), i)
	}
	p1, _ := st.Scan("", 3)
	p2, _ := st.Scan(p1.NextCursor, 3)
	p3, _ := st.Scan(p2.NextCursor, 3)
	all := append(append(append([]string{}, keys(p1)...), keys(p2)...), keys(p3)...)
	p4, _ := st.Scan(p3.NextCursor, 3)
	report("three-pages-no-dup-no-gap", len(all) == 7 && len(p4.Items) == 0 && !p4.HasMore,
		fmt.Sprintf("%v then empty=%v", all, keys(p4)))

	// 2) 翻页中途：靠前插入 + 删除靠后元素，展示变更标记、丢弃与截断的区分。
	d1, _ := st.Scan("", 2)
	st.Put("a00-inserted-before-cursor", 99)
	st.Delete("k05")
	d2, _ := st.Scan(d1.NextCursor, 2)
	d3, _ := st.Scan(d2.NextCursor, 4)
	stats, changes, _ := st.Stats(d3.NextCursor)
	report("mid-scan-insert/delete-markers", changes.Inserted && changes.Deleted && d2.HasMore &&
		d3.Dropped == 1 && !d3.HasMore,
		fmt.Sprintf("page=%v trunc=%v dropped=%d ins/del=%v", keys(d3), d3.HasMore, d3.Dropped, stats))

	// 3) limit=0：空页且游标不前进。
	lst := ontology.NewStore()
	for i := 0; i < 3; i++ {
		lst.Put(fmt.Sprintf("v%02d", i), i)
	}
	z1, _ := lst.Scan("", 0)
	z2, _ := lst.Scan(z1.NextCursor, 1)
	report("limit-zero-empty-no-advance", len(z1.Items) == 0 && len(z2.Items) == 1 && z2.Items[0].Key == "v00",
		fmt.Sprintf("zero=%v then=%v", keys(z1), keys(z2)))

	// 4) limit=-1：参数错误，与空页是两种结果。
	_, negErr := lst.Scan("", -1)
	_, zeroErr := lst.Scan("", 0)
	report("limit-negative-is-error", errors.Is(negErr, ontology.ErrInvalidLimit) && zeroErr == nil,
		fmt.Sprintf("negErr=%v zeroErr=%v", negErr, zeroErr))

	// 5) 篡改游标 vs 跨会话游标：两种不同错误类别。
	tamper := d1.NextCursor
	tampered := tamper[:len(tamper)-3] + "AAA"
	_, errTampered := st.Scan(tampered, 2)
	cross := d2.NextCursor
	_ = st.Invalidate(cross)
	_, errInvalidated := st.Scan(cross, 2)
	report("tamper-vs-invalidated-distinct",
		errors.Is(errTampered, ontology.ErrCursorMalformed) &&
			errors.Is(errInvalidated, ontology.ErrCursorInvalidated) &&
			!errors.Is(errInvalidated, ontology.ErrCursorMalformed),
		fmt.Sprintf("tampered=%v invalidated=%v", errTampered, errInvalidated))

	// 6) 同一游标并发 Scan 两次，返回完全一致。
	cursor := p1.NextCursor
	var ra, rb ontology.Page
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ra, _ = st.Scan(cursor, 3) }()
	go func() { defer wg.Done(); rb, _ = st.Scan(cursor, 3) }()
	wg.Wait()
	same := equal(keys(ra), keys(rb)) && ra.NextCursor == rb.NextCursor
	report("concurrent-same-cursor-idempotent", same, fmt.Sprintf("a=%v b=%v", keys(ra), keys(rb)))

	fmt.Printf("TOTAL  pass=%d fail=%d\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
