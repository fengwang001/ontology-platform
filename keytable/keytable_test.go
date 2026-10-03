package keytable

import (
	"fmt"
	"testing"
)

func mustUpsert(t *testing.T, tb *Table, tn, key string, cur uint64) *Evicted {
	t.Helper()
	_, _, ev, err := tb.Upsert(tn, key, cur)
	if err != nil {
		t.Fatalf("Upsert(%s,%s): %v", tn, key, err)
	}
	return ev
}

// TestLRUEvictionOrder: eviction follows access order, not insertion order.
func TestLRUEvictionOrder(t *testing.T) {
	tb := New(2, 10)
	mustUpsert(t, tb, "t", "a", 0)
	mustUpsert(t, tb, "t", "b", 0)
	mustUpsert(t, tb, "t", "a", 0) // touch a; b is now LRU
	if ev := mustUpsert(t, tb, "t", "c", 0); ev == nil || ev.Key != "b" {
		t.Fatalf("evict order: got %+v, want key b", ev)
	}
	if _, ok := tb.Peek("t", "a"); !ok {
		t.Fatal("a must survive")
	}
	if _, ok := tb.Peek("t", "b"); ok {
		t.Fatal("b must be evicted")
	}
	t.Logf("input: a,b,a(touch),c with Kt=2 -> evicted b (LRU), a kept")
}

// TestTenantIsolation: eviction never crosses tenant boundaries.
func TestTenantIsolation(t *testing.T) {
	tb := New(1, 10)
	mustUpsert(t, tb, "t1", "a", 0)
	mustUpsert(t, tb, "t2", "x", 0)
	if ev := mustUpsert(t, tb, "t2", "y", 0); ev == nil || ev.Key != "x" {
		t.Fatalf("t2 eviction: got %+v, want key x", ev)
	}
	if _, ok := tb.Peek("t1", "a"); !ok {
		t.Fatal("t1/a must not be affected by t2 eviction")
	}
	t.Logf("input: t1/a, t2/x, t2/y with Kt=1 -> only t2/x evicted")
}

// TestTenantLimit: a new tenant beyond Tmax fails without changing state.
func TestTenantLimit(t *testing.T) {
	tb := New(2, 1)
	mustUpsert(t, tb, "t1", "a", 0)
	if _, _, _, err := tb.Upsert("t2", "b", 0); err != ErrTenantLimit {
		t.Fatalf("err = %v, want ErrTenantLimit", err)
	}
	if _, ok := tb.Peek("t1", "a"); !ok {
		t.Fatal("rejected upsert must not change state")
	}
	if _, _, _, err := tb.Upsert("t1", "b", 0); err != nil {
		t.Fatalf("existing tenant must still accept: %v", err)
	}
	t.Logf("input: Tmax=1, new tenant t2 -> ErrTenantLimit, t1 untouched")
}

// TestExaminedConstant: entries examined per Upsert is O(1), independent
// of Kt. Asserted with the non-exported counter at Kt=100 and Kt=10000.
func TestExaminedConstant(t *testing.T) {
	for _, kt := range []int{100, 10000} {
		tb := New(kt, 10)
		for i := 0; i < kt; i++ {
			mustUpsert(t, tb, "t", fmt.Sprintf("k%05d", i), 0)
		}
		before := tb.examined
		const ops = 50
		for i := 0; i < ops; i++ { // hits on existing keys
			mustUpsert(t, tb, "t", fmt.Sprintf("k%05d", i*7%kt), 0)
		}
		hits := tb.examined - before
		if hits != ops {
			t.Fatalf("Kt=%d: hit path examined %d entries for %d ops, want 1 each", kt, hits, ops)
		}
		before = tb.examined
		for i := 0; i < ops; i++ { // misses: each evicts exactly one entry
			mustUpsert(t, tb, "t", fmt.Sprintf("n%05d", i), 0)
		}
		misses := tb.examined - before
		if misses != ops {
			t.Fatalf("Kt=%d: miss path examined %d entries for %d ops, want 1 each", kt, misses, ops)
		}
		t.Logf("Kt=%d: examined/op = 1 for both hit and miss+evict paths (O(1), Kt-independent)", kt)
	}
}
