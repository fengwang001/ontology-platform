package orphanreclaim

import (
	"reflect"
	"sort"
	"sync"
	"testing"
)

type recordingCascade struct {
	mu    sync.Mutex
	calls []cascadeCall
}

type cascadeCall struct {
	obj      string
	outLinks map[string][]string
	at       int64
}

func (c *recordingCascade) OnPurge(obj string, out map[string]map[string]struct{}, at int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := map[string][]string{}
	for tgt, types := range out {
		var ts []string
		for lt := range types {
			ts = append(ts, lt)
		}
		sort.Strings(ts)
		snap[tgt] = ts
	}
	c.calls = append(c.calls, cascadeCall{obj: obj, outLinks: snap, at: at})
}

// 级联：清理 a 时原子摘除 a 的出边；其目标可能因此新成孤儿并进第一代。
func TestPurgeCascadeIsAtomic(t *testing.T) {
	c := &fakeClock{t: 0}
	cas := &recordingCascade{}
	r, err := New(testConfig(), c.now, cas, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "keep", "s"} {
		r.CreateObject(id)
	}

	// a 仅靠 keep 的独立边存活；a -> b 是一条 J1（b 同时还有 s 的 J2，联合保留）。
	mustAdd(t, r, "keep", "a", "I")
	mustAdd(t, r, "a", "b", "J1")
	mustAdd(t, r, "s", "b", "J2")
	assertGen(t, r, "a", GenNone)
	assertGen(t, r, "b", GenNone)

	// keep 消失 -> a 走完两代被清理；a 的出边在同一次清理中原子摘除，
	// b 失去 J1 后联合组破坏，新成孤儿从第一代起算。
	mustRemove(t, r, "keep", "a", "I")
	c.t = 10
	r.Advance() // a -> gen2
	c.t = 14
	r.Advance() // purge a
	assertGone(t, r, "a")
	assertGen(t, r, "b", Gen1)

	if len(cas.calls) != 1 || cas.calls[0].obj != "a" {
		t.Fatalf("cascade calls = %+v", cas.calls)
	}
	want := map[string][]string{"b": {"J1"}}
	if !reflect.DeepEqual(cas.calls[0].outLinks, want) {
		t.Fatalf("cascade snapshot = %v, want %v", cas.calls[0].outLinks, want)
	}
	// a 已不存在，任何地方都不应残留其入/出边索引。
	snap := r.Snapshot()
	if _, ok := snap.OutEdges["a"]; ok {
		t.Fatal("purged object still has out-edge index")
	}
	if srcs := snap.InEdges["b"]; len(srcs) != 1 {
		t.Fatalf("b in-edges after cascade = %v, want only s", srcs)
	}
}
