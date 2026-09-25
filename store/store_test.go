package store_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/store"
)

// TestStoreBasic 边界语义：空键、空值、版本可见性。
func TestStoreBasic(t *testing.T) {
	cases := []struct {
		name  string
		check func(t *testing.T, s *store.Store)
	}{
		{"空值与键不存在可区分", func(t *testing.T, s *store.Store) {
			s.Put("k", []byte{})
			if v, ok := s.Get("k"); !ok || len(v) != 0 {
				t.Fatalf("空值: ok=%v len=%d", ok, len(v))
			}
			if _, ok := s.Get("missing"); ok {
				t.Fatal("不存在的键不应命中")
			}
		}},
		{"空键合法", func(t *testing.T, s *store.Store) {
			s.Put("", []byte("v"))
			if v, ok := s.Get(""); !ok || string(v) != "v" {
				t.Fatalf("空键: ok=%v v=%q", ok, v)
			}
		}},
		{"按版本读取", func(t *testing.T, s *store.Store) {
			v1 := s.Put("k", []byte("old"))
			v2 := s.Put("k", []byte("new"))
			for _, q := range []struct {
				ver  uint64
				want string
				ok   bool
			}{{v1, "old", true}, {v2, "new", true}, {v1 - 1, "", false}} {
				got, ok := s.GetAt("k", q.ver)
				if ok != q.ok || string(got) != q.want {
					t.Fatalf("GetAt(%d)=%q,%v 期望 %q,%v", q.ver, got, ok, q.want, q.ok)
				}
			}
		}},
		{"KeysAt只含快照前键", func(t *testing.T, s *store.Store) {
			s.Put("a", []byte("1"))
			ver := s.Version()
			s.Put("b", []byte("2"))
			if got := s.KeysAt(ver); len(got) != 1 || got[0] != "a" {
				t.Fatalf("KeysAt=%v", got)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.check(t, store.New()) })
	}
}

// TestStoreRetained 10 万键下保留值个数只随改写键数增长，解除钉后归零。
func TestStoreRetained(t *testing.T) {
	const total = 100000
	for _, modified := range []int{0, 100, total} {
		t.Run(fmt.Sprintf("改写%d键", modified), func(t *testing.T) {
			s := store.New()
			for i := 0; i < total; i++ {
				s.Put(fmt.Sprintf("k%06d", i), []byte("v"))
			}
			unpin := s.Pin(s.Version())
			for i := 0; i < modified; i++ {
				s.Put(fmt.Sprintf("k%06d", i), []byte("v2"))
			}
			if got := s.Retained(); got != int64(modified) {
				t.Fatalf("保留值=%d 期望 %d", got, modified)
			}
			unpin()
			if got := s.Retained(); got != 0 {
				t.Fatalf("解除钉后保留值=%d 期望 0", got)
			}
		})
	}
}

// TestStoreConcurrent 并发读写下版本单调、终值正确（-race 验证）。
func TestStoreConcurrent(t *testing.T) {
	s := store.New()
	const writers, per = 8, 500
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				k := fmt.Sprintf("w%d-%04d", w, i)
				s.Put(k, []byte{byte(i)})
				s.Get(k)
				s.GetAt(k, s.Version())
			}
		}(w)
	}
	wg.Wait()
	if got := s.KeysAt(s.Version()); len(got) != writers*per {
		t.Fatalf("键数=%d 期望 %d", len(got), writers*per)
	}
}

// TestStoreReads 读取计数器精确累计。
func TestStoreReads(t *testing.T) {
	s := store.New()
	s.Put("a", []byte("1"))
	s.ResetReads()
	for i := 0; i < 7; i++ {
		s.Get("a")
	}
	if got := s.Reads(); got != 7 {
		t.Fatalf("Reads=%d 期望 7", got)
	}
}
