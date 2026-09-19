package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// 同一游标并发 Scan 多次：页内容完全一致（幂等），且游标不会被各自推进一次。
func TestConcurrentSameCursorIdempotent(t *testing.T) {
	st := seededStore(20)
	first, _ := st.Scan("", 5)
	cursor := first.NextCursor

	const n = 32
	var wg sync.WaitGroup
	pages := make([][]string, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			page, err := st.Scan(cursor, 5)
			errs[idx] = err
			pages[idx] = keysOf(page)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("scan %d error: %v", i, errs[i])
		}
		if len(pages[i]) != 5 {
			t.Fatalf("scan %d len=%d", i, len(pages[i]))
		}
		for j := 0; j < n; j++ {
			if !equalStrings(pages[i], pages[j]) {
				t.Fatalf("pages differ: %v vs %v", pages[i], pages[j])
			}
		}
	}

	// 游标位置只应推进一次：用返回的游标继续，下一页必须紧跟在第一页之后。
	// 任取一个并发返回的游标都等价；这里用第一次的结果继续遍历校验整体无重漏。
	page, _ := st.Scan(cursor, 5)
	all := append(append([]string{}, keysOf(first)...), keysOf(page)...)
	next := page.NextCursor
	for {
		p, err := st.Scan(next, 5)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, keysOf(p)...)
		next = p.NextCursor
		if !p.HasMore {
			break
		}
	}
	if len(all) != 20 {
		t.Fatalf("expected 20 keys with no double-advance, got %d: %v", len(all), all)
	}
}

// Scan 与持续写入并发：不 panic、不死锁，结果始终基于快照、无重复。
func TestScanConcurrentWithWriters(t *testing.T) {
	st := seededStore(10)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 100; i < 300; i++ {
			st.Put("con-"+fmt.Sprintf("%03d", i), i)
		}
		for i := 0; i < 50; i++ {
			st.Delete("con-" + fmt.Sprintf("%03d", 100+i))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		cursor := ""
		seen := map[string]bool{}
		for {
			p, err := st.Scan(cursor, 3)
			if err != nil {
				t.Errorf("scan: %v", err)
				return
			}
			for _, item := range p.Items {
				if seen[item.Key] {
					t.Errorf("duplicate %s", item.Key)
					return
				}
				seen[item.Key] = true
			}
			cursor = p.NextCursor
			if !p.HasMore {
				break
			}
		}
	}()
	wg.Wait()
}

func equalStrings(a, b []string) bool {
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
