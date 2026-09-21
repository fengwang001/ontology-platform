package ttlcache

import (
	"errors"
	"sync"
	"testing"
)

// clock is a manually advanced logical clock for tests.
type clock struct{ t int64 }

func (c *clock) now() int64      { return c.t }
func (c *clock) advance(d int64) { c.t += d }

func newTestCache(t *testing.T, capacity int) (*Cache, *clock) {
	t.Helper()
	clk := &clock{}
	c, err := New(capacity, clk.now)
	if err != nil {
		t.Fatalf("New(%d): %v", capacity, err)
	}
	return c, clk
}

func TestNewRejectsNonPositiveCapacity(t *testing.T) {
	for _, cap := range []int{0, -1, -100} {
		if _, err := New(cap, func() int64 { return 0 }); !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("New(%d) err = %v, want ErrInvalidCapacity", cap, err)
		}
	}
}

func TestPutRejectsNonPositiveTTL(t *testing.T) {
	c, _ := newTestCache(t, 2)
	for _, ttl := range []int64{0, -1, -1000} {
		if err := c.Put("k", "v", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Errorf("Put ttl=%d err = %v, want ErrInvalidTTL", ttl, err)
		}
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0 after rejected puts", c.Len())
	}
}

func TestBasicPutGetDelete(t *testing.T) {
	c, _ := newTestCache(t, 2)
	if err := c.Put("a", "1", 100); err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Errorf("Get(a) = %q,%v, want \"1\",true", v, ok)
	}
	if _, ok := c.Get("missing"); ok {
		t.Error("Get(missing) should miss")
	}
	if !c.Delete("a") {
		t.Error("Delete(a) = false, want true")
	}
	if c.Delete("a") {
		t.Error("Delete(a) again = true, want false")
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
}

func TestConcurrentAccess(t *testing.T) {
	c, _ := newTestCache(t, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + i))
			for j := 0; j < 100; j++ {
				_ = c.Put(key, "v", 1000)
				c.Get(key)
				c.Len()
			}
		}(i)
	}
	wg.Wait()
	if c.Len() != 8 {
		t.Errorf("Len() = %d, want 8", c.Len())
	}
}
