package store_test

import (
	"sync"
	"testing"

	"ontology/store"
)

func TestStore(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		cases := []struct {
			name string
			run  func(s *store.Store) (any, any)
			want any
		}{
			{"empty_get", func(s *store.Store) (any, any) { v, ok := s.Get("k"); return ok, false }, false},
			{"empty_val_exists", func(s *store.Store) (any, any) { s.Put("k", nil); _, ok := s.Get("k"); return ok, nil }, true},
			{"empty_key", func(s *store.Store) (any, any) { s.Put("", []byte("v")); _, ok := s.Get(""); return ok, nil }, true},
			{"overwrite", func(s *store.Store) (any, any) {
				s.Put("k", []byte("a"))
				s.Put("k", []byte("bb"))
				v, _ := s.Get("k")
				return string(v), nil
			}, "bb"},
			{"delete", func(s *store.Store) (any, any) {
				s.Put("k", []byte("a"))
				s.Delete("k")
				_, ok := s.Get("k")
				return ok, nil
			}, false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got, _ := c.run(store.New(nil))
				if got != c.want {
					t.Fatalf("got %v want %v", got, c.want)
				}
			})
		}
	})

	t.Run("version_monotonic", func(t *testing.T) {
		s := store.New(nil)
		vs := []uint64{s.Put("a", []byte("1")), s.Put("b", []byte("2")), s.Delete("a")}
		for i := 1; i < len(vs); i++ {
			if vs[i] <= vs[i-1] {
				t.Fatalf("version not monotonic: %v", vs)
			}
		}
		if s.Version() != 3 {
			t.Fatalf("version=%d", s.Version())
		}
	})

	t.Run("hook_sees_old_value", func(t *testing.T) {
		var mu sync.Mutex
		var olds []string
		s := store.New(func(key string, old store.Entry, newVer uint64) {
			mu.Lock()
			defer mu.Unlock()
			if old.Exist {
				olds = append(olds, string(old.Val))
			}
		})
		s.Put("k", []byte("old"))
		s.Put("k", []byte("new"))
		mu.Lock()
		defer mu.Unlock()
		if len(olds) != 1 || olds[0] != "old" {
			t.Fatalf("olds=%v", olds)
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		s := store.New(nil)
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					s.Put("k", []byte{byte(g), byte(i)})
					s.Get("k")
				}
			}(g)
		}
		wg.Wait()
	})
}
