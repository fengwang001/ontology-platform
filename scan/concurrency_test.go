package scan_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/rule"
	"ontology/scan"
	"ontology/store"
)

// 相同输入重放结果完全相同：同样的 budget 序列跑两次，状态与清单逐项一致。
func TestReplayDeterministic(t *testing.T) {
	run := func() ([]flatItem, string) {
		st := store.New()
		loadKey(t, st, "a", sv(1, 0, false, 0), sv(2, 100000, false, 0), sv(3, 200000, false, 0))
		loadKey(t, st, "b", sv(4, 0, false, 0), sv(5, 300000, false, 0))
		eng, err := rule.New([]rule.Rule{
			{ID: "n", Kind: rule.NoncurrentExpire, Days: 1, Keep: 1},
			{ID: "e", Kind: rule.Expire, Days: 1},
			{ID: "o", Kind: rule.OrphanMarker, Days: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		se := scan.New(st, eng)
		budgets := []int64{1, 1, 2, 1, 3, 1}
		var items []flatItem
		var cursor []byte
		var final string
		for {
			b := budgets[len(items)%len(budgets)]
			out := se.Scan(400000, b, cursor)
			if out.Err != nil {
				t.Fatal(out.Err)
			}
			for _, it := range out.Items {
				items = append(items, flatItem{string(it.Key), it.Ver, int(it.Action)})
			}
			cursor = out.Cursor
			if len(cursor) == 0 {
				for _, k := range st.KeysFrom(nil) {
					vs, _ := st.Snapshot(k)
					final += fmt.Sprintf("%s%v", k, vs)
				}
				return items, final
			}
		}
	}
	a, fa := run()
	b, fb := run()
	if !itemsEqual(toNaive(a), b) || fa != fb {
		t.Fatalf("replay mismatch:\n%v vs %v\n%s vs %s", a, b, fa, fb)
	}
}

func toNaive(f []flatItem) []ni {
	out := make([]ni, len(f))
	for i, x := range f {
		out[i] = ni{key: x.key, ver: x.ver, action: x.action}
	}
	return out
}

// 并发：大量 Scan 并发调用 -race 下无数据竞争，且每个键最终至多被等价处理，
// 全部返回空游标后状态稳定（同 now 重放幂等）。
func TestConcurrentScans(t *testing.T) {
	st := store.New()
	loadKey(t, st, "k1", sv(1, 0, false, 0), sv(2, 50000, false, 0))
	loadKey(t, st, "k2", sv(3, 0, false, 0), sv(4, 50000, false, 0), sv(5, 90000, false, 0))
	loadKey(t, st, "k3", sv(6, 0, false, 0))
	eng, err := rule.New([]rule.Rule{
		{ID: "e", Kind: rule.Expire, Days: 3650}, // now 很小，永不到期
	})
	if err != nil {
		t.Fatal(err)
	}
	se := scan.New(st, eng)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				out := se.Scan(0, 1, nil)
				if out.Err != nil {
					t.Errorf("concurrent scan: %v", out.Err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 状态未变。
	for _, k := range []string{"k1", "k2", "k3"} {
		vs, ok := st.Snapshot([]byte(k))
		if !ok || len(vs) == 0 {
			t.Fatalf("key %s unexpectedly changed", k)
		}
		for _, v := range vs {
			if v.Marker {
				t.Fatalf("no markers expected, got %+v", vs)
			}
		}
	}
}
