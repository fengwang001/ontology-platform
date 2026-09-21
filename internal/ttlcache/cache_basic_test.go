package ttlcache

import (
	"errors"
	"testing"
)

func TestNewInvalidCapacity(t *testing.T) {
	for _, cap_ := range []int{0, -1, -100} {
		if _, err := New(cap_, func() int64 { return 0 }); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d): want ErrInvalidCapacity, got %v", cap_, err)
		}
	}
}

func TestPutInvalidTTL(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	for _, ttl := range []int64{0, -1, -100} {
		if err := c.Put("k", "v", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Fatalf("Put ttl=%d: want ErrInvalidTTL, got %v", ttl, err)
		}
	}
	if c.Len() != 0 {
		t.Fatalf("invalid Put must not store anything, Len=%d", c.Len())
	}
	// 已存在的键用非法 TTL 更新同样报错，且原值不变。
	mustPut(c, "k", "v1", 10)
	if err := c.Put("k", "v2", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("update with ttl=0: want ErrInvalidTTL, got %v", err)
	}
	if v, ok := c.Get("k"); !ok || v != "v1" {
		t.Fatalf("after failed update: got (%q,%v), want (v1,true)", v, ok)
	}
}

func TestPutGetBasic(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	if _, ok := c.Get("missing"); ok {
		t.Fatal("Get on empty cache should miss")
	}
	mustPut(c, "a", "1", 100)
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatalf("Get(a) = (%q,%v), want (1,true)", v, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

func TestDelete(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	if c.Delete("nope") {
		t.Fatal("Delete missing key should return false")
	}
	mustPut(c, "a", "1", 100)
	if !c.Delete("a") {
		t.Fatal("Delete existing key should return true")
	}
	if c.Len() != 0 {
		t.Fatalf("Len after Delete = %d, want 0", c.Len())
	}
	if c.Delete("a") {
		t.Fatal("Delete twice should return false")
	}
}

func TestDeleteExpiredCountsAsDeleted(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "a", "1", 10)
	clk.set(10) // a 已过期但未清理
	if c.Len() != 1 {
		t.Fatalf("expired entry still occupies capacity, Len = %d, want 1", c.Len())
	}
	if !c.Delete("a") {
		t.Fatal("Delete of expired-but-present key should return true")
	}
	if c.Len() != 0 {
		t.Fatalf("Len after Delete = %d, want 0", c.Len())
	}
}
