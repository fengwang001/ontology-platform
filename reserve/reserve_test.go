package reserve

import (
	"sort"
	"testing"

	"ontology"
)

func TestLedgerMergeAndRemove(t *testing.T) {
	l := NewLedger()
	// 同一订单同一库位多次取货合并为一条。
	l.Add("O1", "B1", 5)
	l.Add("O1", "B1", 3)
	l.Add("O1", "B2", 2)
	l.Add("O2", "B1", 4)
	if q, ok := l.Get("O1", "B1"); !ok || q != 8 {
		t.Fatalf("O1/B1 = %d,%v want 8", q, ok)
	}
	if l.CountAt("B1") != 2 {
		t.Fatalf("CountAt B1 = %d want 2", l.CountAt("B1"))
	}
	if q, n := l.Remove("O1", "B1"); q != 8 || n != 1 {
		t.Fatalf("Remove = %d,%d want 8,1", q, n)
	}
	if _, ok := l.Get("O1", "B1"); ok {
		t.Fatal("record still present")
	}
	if l.CountAt("B1") != 1 {
		t.Fatalf("CountAt after remove = %d want 1", l.CountAt("B1"))
	}
	if q, n := l.Remove("O1", "B1"); q != 0 || n != 0 {
		t.Fatalf("Remove missing = %d,%d want 0,0", q, n)
	}
}

func TestLedgerRemoveAllAt(t *testing.T) {
	l := NewLedger()
	l.Add("O1", "K1", 4)
	l.Add("O2", "K1", 5)
	l.Add("O3", "K1", 6)
	l.Add("O1", "B1", 9)
	affected, touched := l.RemoveAllAt("K1")
	if touched != 3 {
		t.Fatalf("touched=%d want 3", touched)
	}
	want := map[ontology.ID]int64{"O1": 4, "O2": 5, "O3": 6}
	for k, v := range want {
		if affected[k] != v {
			t.Fatalf("affected[%s]=%d want %d", k, affected[k], v)
		}
	}
	// O1 在 B1 的记录不受影响。
	if q, ok := l.Get("O1", "B1"); !ok || q != 9 {
		t.Fatalf("O1/B1=%d,%v want 9", q, ok)
	}
	if l.CountAt("K1") != 0 {
		t.Fatalf("K1 still has %d records", l.CountAt("K1"))
	}
	locs := []ontology.ID{}
	for k := range l.LocsOf("O1") {
		locs = append(locs, k)
	}
	sort.Strings(asString(locs))
	if len(locs) != 1 || locs[0] != "B1" {
		t.Fatalf("O1 locs=%v want [B1]", locs)
	}
}

func asString(ids []ontology.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}
