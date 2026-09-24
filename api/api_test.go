package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

func equal(a, b []string) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func TestHitConsistency(t *testing.T) {
	tb := api.New(16, 8)
	_ = tb.Put("Hello", "v1")
	for _, k := range []string{"HELLO", "hello", "HeLLo"} {
		if v, ok := tb.Get(k); !ok || v != "v1" {
			t.Errorf("Get(%q) = %q,%v, want v1,true", k, v, ok)
		}
	}
	if _, ok := tb.Get("world"); ok {
		t.Error("distinct folded key must not hit")
	}
}

func TestOrigKeyPreserved(t *testing.T) {
	tb := api.New(16, 8)
	_ = tb.Put("GoLang", "1")
	_ = tb.Put("golang", "2")
	if k := tb.Keys(); len(k) != 1 || k[0] != "GoLang" {
		t.Errorf("Keys() = %v, want [GoLang]", k)
	}
	if v, _ := tb.Get("GOLANG"); v != "2" {
		t.Errorf("Get(GOLANG) = %q, want 2", v)
	}
}

func TestKeysOrderDeterministic(t *testing.T) {
	for _, ord := range [][]string{{"Banana", "apple", "CHERRY"}, {"CHERRY", "Banana", "apple"}} {
		tb := api.New(16, 8)
		for _, k := range ord {
			_ = tb.Put(k, "x")
		}
		if got, want := tb.Keys(), []string{"apple", "Banana", "CHERRY"}; !equal(got, want) {
			t.Errorf("insert %v: Keys() = %v, want %v", ord, got, want)
		}
	}
}

func TestRejectedOps(t *testing.T) {
	if api.ErrEmptyKey == api.ErrKeyLong || api.ErrKeyLong == api.ErrFull || api.ErrEmptyKey == api.ErrFull {
		t.Fatal("sentinel errors must be distinct")
	}
	names := []string{"empty", "too long"}
	keys := []string{"", "12345678901234567"}
	werrs := []error{api.ErrEmptyKey, api.ErrKeyLong}
	for i, key := range keys {
		tb := api.New(16, 8)
		_ = tb.Put("keep", "v")
		before := tb.Keys()
		if err := tb.Put(key, "x"); !errors.Is(err, werrs[i]) {
			t.Fatalf("%s: err = %v, want %v", names[i], err, werrs[i])
		}
		if !equal(tb.Keys(), before) || tb.SelfCheck() != nil || tb.Put("ok", "1") != nil {
			t.Fatalf("%s: rejection left trace or broke table", names[i])
		}
	}
	tb := api.New(16, 1)
	_ = tb.Put("one", "1")
	if err := tb.Put("two", "2"); !errors.Is(err, api.ErrFull) || len(tb.Keys()) != 1 || tb.SelfCheck() != nil {
		t.Fatalf("overflow: err = %v, keys = %v", err, tb.Keys())
	}
	if err := tb.Put("ONE", "2"); err != nil {
		t.Fatal("update of existing folded key must succeed")
	}
}

func TestConcurrentReads(t *testing.T) {
	tb := api.New(16, 8)
	_ = tb.Put("Alpha", "v")
	want := tb.Keys()
	var wg sync.WaitGroup
	bad := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, ok := tb.Get("ALPHA")
			if !ok || v != "v" || !equal(tb.Keys(), want) || tb.SelfCheck() != nil {
				bad <- "inconsistent read"
			}
		}()
	}
	wg.Wait()
	close(bad)
	if len(bad) > 0 {
		t.Error(<-bad)
	}
}
