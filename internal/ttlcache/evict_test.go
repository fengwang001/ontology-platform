package ttlcache

import "testing"

// Rule 4, scenario 1: capacity 2, a(TTL 10) and b(TTL 100); at t=15
// writing c evicts the expired a and keeps b.
func TestEvictPrefersExpired(t *testing.T) {
	c, clk := newTestCache(t, 2)
	_ = c.Put("a", "1", 10)
	_ = c.Put("b", "2", 100)
	clk.advance(15) // a expired, b alive
	_ = c.Put("c", "3", 100)

	if _, ok := c.Get("a"); ok {
		t.Error("a should have been evicted (expired)")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("b should survive")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("c should be present")
	}
	if got := c.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

// Rule 4, scenario 2: nothing expired, Get(a) then Put(c) evicts b (LRU).
func TestEvictLRUWhenNothingExpired(t *testing.T) {
	c, _ := newTestCache(t, 2)
	_ = c.Put("a", "1", 100)
	_ = c.Put("b", "2", 100)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("Get(a) should hit")
	}
	_ = c.Put("c", "3", 100)

	if _, ok := c.Get("b"); ok {
		t.Error("b should have been evicted as LRU")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a should survive")
	}
}

// Rule 4, scenario 3: both expired; the one written earliest is
// evicted, regardless of recency of access. Assertions use Delete/Len
// so they do not trigger Get's expired-item cleanup.
func TestEvictEarliestWrittenAmongExpired(t *testing.T) {
	c, clk := newTestCache(t, 2)
	_ = c.Put("a", "1", 10) // written t=0, expires t=10
	clk.advance(1)
	_ = c.Put("b", "2", 30) // written t=1, expires t=31
	clk.advance(8)          // t=9: b still valid
	if _, ok := c.Get("b"); !ok {
		t.Fatal("Get(b) should hit at t=9")
	} // b is now the most recently used
	clk.advance(22) // t=31: both expired
	_ = c.Put("c", "3", 100)

	if c.Delete("a") {
		t.Error("a (written earliest) should have been evicted")
	}
	if !c.Delete("b") {
		t.Error("b (written later) should survive despite being stale and MRU")
	}
	if got := c.Len(); got != 1 { // only c remains
		t.Errorf("Len() = %d, want 1", got)
	}
}

// Expired items are not evicted eagerly: with free capacity, Put must
// not remove them.
func TestNoEvictionWhenNotFull(t *testing.T) {
	c, clk := newTestCache(t, 3)
	_ = c.Put("a", "1", 10)
	clk.advance(10) // a expired
	_ = c.Put("b", "2", 100)
	if got := c.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2 (stale a must be kept while not full)", got)
	}
	if !c.Delete("a") {
		t.Error("stale a should still be deletable")
	}
}
