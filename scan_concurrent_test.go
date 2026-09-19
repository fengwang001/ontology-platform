package ontology

import (
	"reflect"
	"sync"
	"testing"
)

// TestConcurrentSameCursorIdempotent 同一游标并发 Scan：
// 两次返回页完全一致，且统计不会被重复推进。
func TestConcurrentSameCursorIdempotent(t *testing.T) {
	s := seedStore(20)
	p1, err := s.Scan("", 5)
	if err != nil {
		t.Fatal(err)
	}

	const n = 16
	var wg sync.WaitGroup
	results := make([][]string, n)
	errs := make([]error, n)
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			p, err := s.Scan(p1.Cursor, 5)
			results[idx] = keys(p)
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		want := []string{"k005", "k006", "k007", "k008", "k009"}
		if !reflect.DeepEqual(results[i], want) {
			t.Fatalf("worker %d got %v", i, results[i])
		}
	}

	st := s.Stats(p1.SessionID())
	if st.Returned != 10 {
		t.Fatalf("Returned=%d want 10 (cursor must advance accounting once)", st.Returned)
	}
}

// TestConcurrentScanWithWriters 遍历进行中其他 goroutine 持续写入，
// 不 panic、不死锁、页元素不重复。
func TestConcurrentScanWithWriters(t *testing.T) {
	s := seedStore(50)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() { // 插入
		defer wg.Done()
		for i := 1000; ; i++ {
			select {
			case <-stop:
				return
			default:
				s.Put(keyOf(i), i)
			}
		}
	}()
	go func() { // 删除未翻到的元素
		defer wg.Done()
		for i := 10; i < 50; {
			select {
			case <-stop:
				return
			default:
				s.Delete(keyOf(i))
				i += 2
			}
		}
	}()

	cursor := ""
	seen := map[string]bool{}
	pages := 0
	var last Page
	for {
		p, err := s.Scan(cursor, 7)
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatal(err)
		}
		for _, o := range p.Objects {
			if seen[o.Key] {
				close(stop)
				wg.Wait()
				t.Fatalf("duplicate key across pages: %s", o.Key)
			}
			seen[o.Key] = true
		}
		pages++
		if !p.HasMore {
			last = p
			break
		}
		cursor = p.Cursor
	}
	close(stop)
	wg.Wait()

	st := s.Stats(last.SessionID())
	if !st.Valid {
		t.Fatal("session should remain valid")
	}
	rep := s.Skipped(last.SessionID())
	if rep.Total != rep.Reasons.DiscardedByDelete {
		t.Fatalf("skip accounting mismatch: %+v", rep)
	}
	if pages == 0 || len(seen) == 0 {
		t.Fatal("expected to traverse something")
	}
}
