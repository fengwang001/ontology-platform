package ttlcache

import (
	"errors"
	"testing"
)

func TestNewRejectsNonPositiveCapacity(t *testing.T) {
	clk := &clock{}
	for _, capacity := range []int{0, -1, -100} {
		c, err := New(capacity, clk.now)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d) err = %v, 期望 ErrInvalidCapacity", capacity, err)
		}
		if c != nil {
			t.Fatalf("New(%d) 应返回 nil 缓存", capacity)
		}
	}
}

func TestNewRejectsNilClock(t *testing.T) {
	if _, err := New(1, nil); !errors.Is(err, ErrNilClock) {
		t.Fatalf("New(1, nil) err = %v, 期望 ErrNilClock", err)
	}
}

func TestPutRejectsNonPositiveTTL(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	for _, ttl := range []int64{0, -1, -1000} {
		if err := c.Put("k", "v", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Fatalf("Put(ttl=%d) err = %v, 期望 ErrInvalidTTL", ttl, err)
		}
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len() = %d, 期望 0（非法 TTL 不应写入）", got)
	}
}

// 同键更新时传入非法 TTL 也应报错，且原值不受影响。
func TestPutInvalidTTLDoesNotTouchExisting(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "v1", 100)
	if err := c.Put("a", "v2", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("Put(同键, ttl=0) err = %v, 期望 ErrInvalidTTL", err)
	}
	mustGet(t, c, "a", "v1")
}
