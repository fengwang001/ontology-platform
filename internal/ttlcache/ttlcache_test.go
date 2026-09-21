package ttlcache

import (
	"errors"
	"testing"
)

// clock 是测试用的手动逻辑时钟。
type clock struct{ now int64 }

func (c *clock) Now() int64      { return c.now }
func (c *clock) Advance(d int64) { c.now += d }

func newCache(t *testing.T, capacity int, c *clock) *Cache {
	t.Helper()
	cache, err := New(capacity, c.Now)
	if err != nil {
		t.Fatalf("New(%d) error: %v", capacity, err)
	}
	return cache
}

func mustPut(t *testing.T, c *Cache, key, val string, ttl int64) {
	t.Helper()
	if err := c.Put(key, val, ttl); err != nil {
		t.Fatalf("Put(%q) error: %v", key, err)
	}
}

func TestNewRejectsInvalidArgs(t *testing.T) {
	c := &clock{}
	for _, capacity := range []int{0, -1, -100} {
		if _, err := New(capacity, c.Now); !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("New(%d) err = %v, want ErrInvalidCapacity", capacity, err)
		}
	}
	if _, err := New(1, nil); !errors.Is(err, ErrNilClock) {
		t.Errorf("New(1, nil) err = %v, want ErrNilClock", err)
	}
}

func TestPutRejectsNonPositiveTTL(t *testing.T) {
	c := newCache(t, 2, &clock{})
	for _, ttl := range []int64{0, -1, -1000} {
		if err := c.Put("k", "v", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Errorf("Put ttl=%d err = %v, want ErrInvalidTTL", ttl, err)
		}
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0 after rejected puts", c.Len())
	}
}

func TestPutGetBasic(t *testing.T) {
	c := newCache(t, 2, &clock{})
	mustPut(t, c, "a", "1", 100)

	val, ok := c.Get("a")
	if !ok || val != "1" {
		t.Errorf("Get(a) = %q, %v; want %q, true", val, ok, "1")
	}
	if _, ok := c.Get("missing"); ok {
		t.Error("Get(missing) ok = true, want false")
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}
}

func TestPutExistingUpdatesValue(t *testing.T) {
	c := newCache(t, 2, &clock{})
	mustPut(t, c, "a", "1", 100)
	mustPut(t, c, "a", "2", 100)

	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}
	if val, ok := c.Get("a"); !ok || val != "2" {
		t.Errorf("Get(a) = %q, %v; want %q, true", val, ok, "2")
	}
}

func TestDelete(t *testing.T) {
	c := newCache(t, 2, &clock{})
	mustPut(t, c, "a", "1", 100)

	if !c.Delete("a") {
		t.Error("Delete(a) = false, want true")
	}
	if c.Delete("a") {
		t.Error("Delete(a) again = true, want false")
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) after delete ok = true, want false")
	}
}

func TestLenCountsUncleanedExpired(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 3, clk)
	mustPut(t, c, "a", "1", 10)

	clk.Advance(10) // a 已过期但未被清理
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (expired entry still occupies capacity)", c.Len())
	}
}
