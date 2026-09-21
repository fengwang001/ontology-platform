package ttlcache

import "testing"

// Rule 1: written at t0 with TTL d, the item is valid at t0+d-1
// and already expired at t0+d.
func TestExpiryDeadline(t *testing.T) {
	c, clk := newTestCache(t, 4)
	if err := c.Put("a", "v", 10); err != nil {
		t.Fatal(err)
	}
	clk.advance(9) // t0+9
	if _, ok := c.Get("a"); !ok {
		t.Error("at t0+9 the item should still be valid")
	}
	clk.advance(1) // t0+10
	if _, ok := c.Get("a"); ok {
		t.Error("at t0+10 the item should be expired")
	}
}

// Rule 2 & 3: expired items still occupy capacity and count toward
// Len() until accessed (Get removes them) or evicted.
func TestExpiredItemCountsAndIsCleanedOnGet(t *testing.T) {
	c, clk := newTestCache(t, 4)
	if err := c.Put("a", "v", 10); err != nil {
		t.Fatal(err)
	}
	clk.advance(10) // a expired
	if got := c.Len(); got != 1 {
		t.Errorf("Len() with stale item = %d, want 1", got)
	}
	if _, ok := c.Get("a"); ok {
		t.Error("Get on expired item should miss")
	}
	if got := c.Len(); got != 0 {
		t.Errorf("Len() after Get cleanup = %d, want 0", got)
	}
}

// Rule 5: Put on an existing key updates the value and promotes it,
// but does NOT refresh the TTL.
func TestPutExistingDoesNotRefreshTTL(t *testing.T) {
	c, clk := newTestCache(t, 4)
	if err := c.Put("a", "old", 10); err != nil {
		t.Fatal(err)
	}
	clk.advance(5)
	if err := c.Put("a", "new", 100); err != nil { // TTL arg ignored
		t.Fatal(err)
	}
	clk.advance(4) // t=9
	if v, ok := c.Get("a"); !ok || v != "new" {
		t.Errorf("Get(a) at t=9 = %q,%v, want \"new\",true", v, ok)
	}
	clk.advance(1) // t=10, original expiry reached
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) at t=10 should miss: TTL must not be refreshed")
	}
}

// Rule 9: deleting an expired but not yet cleaned item returns true.
func TestDeleteExpiredItemReturnsTrue(t *testing.T) {
	c, clk := newTestCache(t, 4)
	if err := c.Put("a", "v", 10); err != nil {
		t.Fatal(err)
	}
	clk.advance(10)
	if !c.Delete("a") {
		t.Error("Delete of stale item = false, want true")
	}
	if got := c.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}
}

// Rule 6: a successful Get promotes the item to most recently used.
func TestGetPromotes(t *testing.T) {
	c, _ := newTestCache(t, 2)
	_ = c.Put("a", "1", 100)
	_ = c.Put("b", "2", 100)
	if _, ok := c.Get("a"); !ok { // a is now MRU, b is LRU
		t.Fatal("Get(a) should hit")
	}
	_ = c.Put("c", "3", 100) // evicts b
	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a should survive: it was promoted by Get")
	}
}
