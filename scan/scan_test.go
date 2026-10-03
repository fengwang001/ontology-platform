package scan_test

import (
	"errors"
	"testing"

	"ontology/rule"
	"ontology/scan"
	"ontology/store"
)

func loadKey(t *testing.T, st *store.Store, key string, vs ...store.Version) {
	t.Helper()
	if err := st.Load([]byte(key), vs); err != nil {
		t.Fatalf("Load(%s): %v", key, err)
	}
}

func sv(ver, c int64, marker bool, r int64) store.Version {
	return store.Version{Ver: ver, C: c, Marker: marker, R: r}
}

// 规格例子的端到端分批扫描：budget=1 也必须与无穷预算逐项一致。
func TestScanSpecExampleEndToEnd(t *testing.T) {
	newEng := func(keep int) *rule.Engine {
		eng, err := rule.New([]rule.Rule{
			{ID: "R1", Kind: rule.NoncurrentExpire, Days: 1, Keep: keep},
			{ID: "R2", Kind: rule.Expire, Days: 1},
			{ID: "R3", Kind: rule.OrphanMarker, Days: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		return eng
	}

	mkStore := func() *store.Store {
		st := store.New()
		loadKey(t, st, "a", sv(1, 0, false, 0), sv(2, 100000, false, 0), sv(3, 200000, false, 0))
		return st
	}

	t.Run("infinite keep=1 now=400000", func(t *testing.T) {
		st := mkStore()
		out := scan.New(st, newEng(1)).Scan(400000, 1_000_000_000, nil)
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		assertVerActions(t, out.Items, [][2]int{{4, 0}, {1, 1}, {2, 1}})
		if len(out.Cursor) != 0 {
			t.Fatalf("cursor should be empty, got %q", out.Cursor)
		}
		if out.Examined() != 3 {
			t.Fatalf("examined %d, want 3", out.Examined())
		}
		vs, _ := st.Snapshot([]byte("a"))
		if len(vs) != 2 || vs[0].Ver != 3 || vs[1].Ver != 4 || !vs[1].Marker {
			t.Fatalf("final versions wrong: %+v", vs)
		}
	})

	t.Run("budget=1 chunked keep=0 now=500000 orphan cascade", func(t *testing.T) {
		st := mkStore()
		se := scan.New(st, newEng(0))
		var all []rule.Item
		var cursor []byte
		for {
			out := se.Scan(500000, 1, cursor)
			if out.Err != nil {
				t.Fatal(out.Err)
			}
			all = append(all, out.Items...)
			// 唯一键代价 3：首个键必处理，examined 允许超 budget。
			if out.Examined() != 3 {
				t.Fatalf("examined %d, want 3", out.Examined())
			}
			cursor = out.Cursor
			if len(cursor) == 0 {
				break
			}
		}
		assertVerActions(t, all, [][2]int{{4, 0}, {1, 1}, {2, 1}, {3, 1}, {4, 1}})
		if _, ok := st.Snapshot([]byte("a")); ok {
			t.Fatal("orphan cleanup should remove the whole key")
		}
	})
}

// budget 记账、首个键超限必处理、游标推进与多键停点。
func TestScanBudgetAccountingAndCursor(t *testing.T) {
	st := store.New()
	loadKey(t, st, "k1", sv(1, 0, false, 0))                                         // 代价 1
	loadKey(t, st, "k2", sv(2, 0, false, 0), sv(3, 0, false, 0))                     // 代价 2
	loadKey(t, st, "k3", sv(4, 0, false, 0), sv(5, 0, false, 0), sv(6, 0, false, 0)) // 3
	eng, err := rule.New([]rule.Rule{{ID: "x", Kind: rule.Expire, Days: 3650}})
	if err != nil {
		t.Fatal(err)
	}
	se := scan.New(st, eng)

	out := se.Scan(0, 3, nil) // 无版本到期，但代价照计：k1(1)+k2(2)=3，停在 k3
	if out.Err != nil || len(out.Items) != 0 {
		t.Fatalf("unexpected: err=%v items=%d", out.Err, len(out.Items))
	}
	if string(out.Cursor) != "k3" || out.Examined() != 3 {
		t.Fatalf("cursor=%q examined=%d", out.Cursor, out.Examined())
	}

	out = se.Scan(0, 2, out.Cursor) // k3 代价 3 为首个键，超限也处理
	if err := out.Err; err != nil {
		t.Fatal(err)
	}
	if len(out.Cursor) != 0 || out.Examined() != 3 {
		t.Fatalf("cursor=%q examined=%d", out.Cursor, out.Examined())
	}
}

// Remove 注入失败：整键撤销（含已追加标记），清单只保留此前键；
// 注入消耗后从同键重试成功。
func TestScanRemoveFailureRollsBackWholeKey(t *testing.T) {
	eng, err := rule.New([]rule.Rule{
		{ID: "n", Kind: rule.NoncurrentExpire, Days: 1, Keep: 1},
		{ID: "e", Kind: rule.Expire, Days: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := store.New()
	loadKey(t, st, "a", sv(1, 0, false, 0)) // 当前到期 → 追加标记，无删除
	loadKey(t, st, "b", sv(2, 0, false, 0), sv(3, 200000, false, 0))
	// b：Expire 追加 v4；阶段二 v2 到期删除（第 1 次 Remove 即失败）。
	st.FailNext(1)

	se := scan.New(st, eng)
	out := se.Scan(500000, 10, nil)
	if !errors.Is(out.Err, store.ErrRemoveFailed) {
		t.Fatalf("want ErrRemoveFailed, got %v", out.Err)
	}
	if string(out.Cursor) != "b" {
		t.Fatalf("cursor should point at failed key, got %q", out.Cursor)
	}
	// a 的标记保留在清单与存储中。
	assertVerActions(t, out.Items, [][2]int{{4, 0}})
	vsA, _ := st.Snapshot([]byte("a"))
	if len(vsA) != 2 || !vsA[1].Marker {
		t.Fatalf("key a effect lost: %+v", vsA)
	}
	// b 整键撤销：仍是 v2,v3 两个数据版本，无 v4 标记。
	vsB, _ := st.Snapshot([]byte("b"))
	if len(vsB) != 2 || vsB[0].Ver != 2 || vsB[1].Ver != 3 {
		t.Fatalf("key b not rolled back: %+v", vsB)
	}
	if st.MaxVer() != 4 {
		t.Fatalf("maxVer after rollback = %d, want 4 (a's marker)", st.MaxVer())
	}

	// 注入已消耗：重试 b 成功（v4 标记 + 删 v2）。
	out = se.Scan(500000, 10, out.Cursor)
	if out.Err != nil {
		t.Fatal(out.Err)
	}
	assertVerActions(t, out.Items, [][2]int{{5, 0}, {2, 1}})
	if len(out.Cursor) != 0 {
		t.Fatalf("cursor %q", out.Cursor)
	}
	vsB, _ = st.Snapshot([]byte("b"))
	if len(vsB) != 2 || vsB[0].Ver != 3 || vsB[1].Ver != 5 || !vsB[1].Marker {
		t.Fatalf("key b retry state wrong: %+v", vsB)
	}
}

func TestScanBadBudget(t *testing.T) {
	se := scan.New(store.New(), mustEngine(t))
	for _, b := range []int64{0, -1, 1_000_000_001} {
		out := se.Scan(0, b, nil)
		if out.Err == nil {
			t.Fatalf("budget %d should be rejected", b)
		}
	}
}

func mustEngine(t *testing.T) *rule.Engine {
	t.Helper()
	eng, err := rule.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func assertVerActions(t *testing.T, items []rule.Item, want [][2]int) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("items len = %d, want %d: %+v", len(items), len(want), items)
	}
	for i, it := range items {
		if it.Ver != int64(want[i][0]) || int(it.Action) != want[i][1] {
			t.Fatalf("item %d = (ver=%d action=%d), want (ver=%d action=%d); all=%+v",
				i, it.Ver, it.Action, int64(want[i][0]), want[i][1], items)
		}
	}
}
