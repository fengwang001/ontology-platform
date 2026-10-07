package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentPaginationAndMutation：并发分页 + 并发变更下，
// 每个续读标记必须锚定唯一快照时点：同标记的并发重放结果逐字节相同，
// 任一完整分页过程得到的对象集合都等于某个历史快照上的朴素结果。
func TestConcurrentPaginationAndMutation(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for i := 0; i < 12; i++ {
		st.AddObject(Object{ID: ObjectID(idName(i))})
	}
	// 链 na -> nb -> nc ...，深度 6、扇出 2，保证多页。
	for i := 0; i < 11; i++ {
		st.AddLink(Link{Type: "out", From: ObjectID(idName(i)), To: ObjectID(idName(i + 1))})
	}

	const workers = 16
	var wg sync.WaitGroup
	results := make([][]ObjectID, workers)
	errorsCh := make(chan error, workers)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			tr := NewTraverser(st)
			p := TraverseParams{Start: "na", MaxDepth: 6, MaxFanout: 2, PageSize: 2}
			var all []ObjectID
			for {
				page, err := tr.Traverse(nil, p)
				if err != nil {
					errorsCh <- err
					return
				}
				all = append(all, page.Objects...)
				if page.NextToken == "" {
					break
				}
				p.Token = page.NextToken
			}
			results[seed] = all
		}(w)
	}

	// 并发变更：不断新增与删除噪声对象（不触碰遍历锚定快照）。
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			select {
			case <-stop:
				return
			default:
				id := ObjectID("noise" + idName(i%50))
				if i%2 == 0 {
					st.AddObject(Object{ID: id})
				} else {
					st.DeleteObject(id)
				}
				i++
			}
		}
	}()

	wg.Wait()
	close(stop)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatalf("concurrent traverse error: %v", err)
	}

	// 所有 worker 起点都是初始链（起点在后续快照中依然存在），
	// 每个标记各自锚定其首次请求的快照；该链在所有相关快照中前缀稳定，
	// 因此完整结果必须一致。
	want := ids("na", "nb", "nc", "nd", "ne", "nf", "ng")
	for w, got := range results {
		if !equalIDs(got, want) {
			t.Fatalf("worker %d drifted: %v", w, got)
		}
	}
}

// TestConcurrentSameTokenReplay：同一标记被多个 goroutine 并发重放，
// 结果必须完全一致（标记时点不漂移）。
func TestConcurrentSameTokenReplay(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for i := 0; i < 8; i++ {
		st.AddObject(Object{ID: ObjectID(idName(i))})
		st.AddLink(Link{Type: "out", From: "na", To: ObjectID(idName(i))})
	}
	tr := NewTraverser(st)
	first, err := tr.Traverse(nil, TraverseParams{Start: "na", MaxDepth: 1, MaxFanout: 8, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}

	const n = 32
	var wg sync.WaitGroup
	pages := make([][]ObjectID, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := tr.Traverse(nil, TraverseParams{Token: first.NextToken})
			if err != nil {
				t.Errorf("replay: %v", err)
				return
			}
			pages[i] = p.Objects
		}(i)
	}

	// 并发写噪声。
	for j := 0; j < 200; j++ {
		st.AddObject(Object{ID: ObjectID("x" + idName(j))})
	}

	wg.Wait()
	for i := 1; i < n; i++ {
		if !equalIDs(pages[0], pages[i]) {
			t.Fatalf("replay %d drifted: %v vs %v", i, pages[0], pages[i])
		}
	}
}
