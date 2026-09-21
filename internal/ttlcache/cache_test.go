package ttlcache

import (
	"errors"
	"testing"
)

// clock 是测试用的可控逻辑时钟。
type clock struct{ t int64 }

func (c *clock) now() int64     { return c.t }
func (c *clock) advance(d int64) { c.t += d }

func newCache(t *testing.T, clk *clock, capacity int) *Cache {
	t.Helper()
	c, err := New(capacity, clk.now)
	if err != nil {
		t.Fatalf("New(%d) failed: %v", capacity, err)
	}
	return c
}

func TestNewRejectsNonPositiveCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1, -100} {
		if _, err := New(capacity, func() int64 { return 0 }); !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("New(%d) err = %v, want ErrInvalidCapacity", capacity, err)
		}
	}
}

func TestPutRejectsNonPositiveTTL(t *testing.T) {
	clk := &clock{}
	c := newCache(t, clk, 2)
	for _, ttl := range []int64{0, -1, -100} {
		if err := c.Put("k", "v", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Errorf("Put ttl=%d err = %v, want ErrInvalidTTL", ttl, err)
		}
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0 after rejected puts", c.Len())
	}
}

func TestExpiryBoundary(t *testing.T) {
	clk := &clock{}
	c := newCache(t, clk, 4)
	if err := c.Put("a", "va", 10); err != nil {
		t.Fatal(err)
	}
	clk.t = 9 // t0+d-1：仍有效
	if v, ok := c.Get("a"); !ok || v != "va" {
		t.Errorf("at t=9 Get(a) = %q,%v, want va,true", v, ok)
	}
	clk.t = 10 // t0+d：已过期
	if _, ok := c.Get("a"); ok {
		t.Error("at t=10 Get(a) should miss (expired at deadline)")
	}
}

func TestExpiredItemCountsInLenUntilTouched(t *testing.T) {
	clk := &clock{}
	c := newCache(t, clk, 4)
	_ = c.Put("a", "va", 10)
	clk.t = 100 // 已过期但未清理
	if n := c.Len(); n != 1 {
		t.Errorf("Len() = %d, want 1 (expired item still occupies capacity)", n)
	}
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) should miss on expired item")
	}
	if n := c.Len(); n != 0 {
		t.Errorf("Len() = %d, want 0 after Get purged expired item", n)
	}
}

func TestPutExistingDoesNotRefreshTTL(t *testing.T) {
	clk := &clock{}
	c := newCache(t, clk, 4)
	_ = c.Put("a", "old", 10)
	clk.t = 5
	if err := c.Put("a", "new", 100); err != nil {
		t.Fatal(err)
	}
	clk.t = 9
	if v, ok := c.Get("a"); !ok || v != "new" {
		t.Errorf("at t=9 Get(a) = %q,%v, want new,true", v, ok)
	}
	clk.t = 10
	if _, ok := c.Get("a"); ok {
		t.Error("at t=10 Get(a) should miss: TTL must not be refreshed by update")
	}
}

func TestDelete(t *testing.T) {
	clk := &clock{}
	c := newCache(t, clk, 4)
	if c.Delete("missing") {
		t.Error("Delete(missing) = true, want false")
	}
	_ = c.Put("a", "va", 10)
	clk.t = 50 // 已过期但未清理
	if !c.Delete("a") {
		t.Error("Delete(expired-but-present) = false, want true")
	}
	if n := c.Len(); n != 0 {
		t.Errorf("Len() = %d, want 0", n)
	}
	if c.Delete("a") {
		t.Error("Delete(a) again = true, want false")
	}
}

func TestGetMissingKey(t *testing.T) {
	clk := &clock{}
	c := newCache(t, clk, 4)
	if v, ok := c.Get("nope"); ok || v != "" {
		t.Errorf("Get(nope) = %q,%v, want \"\",false", v, ok)
	}
}
