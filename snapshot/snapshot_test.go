package snapshot_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/snapshot"
	"ontology/store"
)

func TestSnapshotSemantics(t *testing.T) {
	type tc struct {
		name string
		run  func(s *store.Store) error
		want error
	}
	cases := []tc{
		{"cow-old-value", func(s *store.Store) error {
			s.Put("k", []byte("old"))
			sn := snapshot.Take(s)
			defer sn.Close()
			s.Put("k", []byte("new"))
			v, ok, _ := sn.Get("k")
			if !ok || string(v) != "old" {
				return fmt.Errorf("got %q,%v want old", v, ok)
			}
			v, ok = s.GetAt("k", s.Current())
			if !ok || string(v) != "new" {
				return fmt.Errorf("store got %q want new", v)
			}
			return nil
		}, nil},
		{"tombstone-vs-absent", func(s *store.Store) error {
			s.Put("k", []byte("v"))
			sn := snapshot.Take(s)
			s.Delete("k")
			v, ok, _ := sn.Get("k")
			if !ok || string(v) != "v" {
				return fmt.Errorf("snapshot lost deleted value")
			}
			if _, ok = s.GetAt("k", s.Current()); ok {
				return fmt.Errorf("deleted key still present")
			}
			sn.Close()
			return nil
		}, nil},
		{"empty-key", func(s *store.Store) error {
			s.Put("", []byte("x"))
			sn := snapshot.Take(s)
			defer sn.Close()
			ks, err := sn.Keys()
			if err != nil || len(ks) != 1 || ks[0] != "" {
				return fmt.Errorf("empty key not visible: %v %v", ks, err)
			}
			return nil
		}, nil},
		{"empty-value-distinct", func(s *store.Store) error {
			s.Put("a", []byte(""))
			sn := snapshot.Take(s)
			defer sn.Close()
			v, ok, err := sn.Get("a")
			if err != nil || !ok || len(v) != 0 {
				return fmt.Errorf("empty value/absent not distinguished: ok=%v", ok)
			}
			if _, ok, _ := sn.Get("missing"); ok {
				return fmt.Errorf("missing key reported present")
			}
			return nil
		}, nil},
		{"read-after-close", func(s *store.Store) error {
			sn := snapshot.Take(s)
			sn.Close()
			if _, _, err := sn.Get("x"); !errors.Is(err, snapshot.ErrClosed) {
				return fmt.Errorf("want ErrClosed got %v", err)
			}
			if _, err := sn.Keys(); !errors.Is(err, snapshot.ErrClosed) {
				return fmt.Errorf("want ErrClosed got %v", err)
			}
			return nil
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(store.New()); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		})
	}
}

func TestRetainRelease(t *testing.T) {
	s := store.New()
	s.Put("k1", []byte("a"))
	s.Put("k2", []byte("b"))
	old := snapshot.Take(s)
	s.Put("k1", []byte("a2"))
	if n := old.RetainedCount(); n != 1 {
		t.Fatalf("first retain count=%d want 1", n)
	}
	young := snapshot.Take(s)
	s.Put("k2", []byte("b2"))
	if n := old.RetainedCount(); n != 2 {
		t.Fatalf("old retains=%d want 2", n)
	}
	if n := young.RetainedCount(); n != 1 {
		t.Fatalf("young retains=%d want 1 (k2 overwrite visible; k1 born before snapshot)", n)
	}
	old.Close()
	if n := old.RetainedCount(); n != 0 {
		t.Fatalf("after close retains=%d want 0", n)
	}
	if n := young.RetainedCount(); n != 1 {
		t.Fatalf("young retains=%d want 1 after overlapping snapshot closed", n)
	}
	young.Close()
	if n := young.RetainedCount(); n != 0 {
		t.Fatalf("after both closed retains=%d want 0", n)
	}
}

func TestResourceBounds(t *testing.T) {
	s := store.New()
	for i := 0; i < 100_000; i++ {
		s.Put(fmt.Sprintf("key-%06d", i), []byte("v"))
	}
	sn := snapshot.Take(s)
	for i := 0; i < 100; i++ {
		s.Put(fmt.Sprintf("key-%06d", i), []byte("V"))
	}
	keys, _ := sn.Keys()
	var ok int64
	for _, k := range keys {
		if v, present, err := sn.Get(k); err != nil || !present {
			t.Fatalf("get %s: %v present=%v", k, err, present)
		} else if string(v) == "V" {
			atomic.AddInt64(&ok, 1)
		}
	}
	if n := sn.RetainedCount(); n > 100 {
		t.Errorf("retained=%d > 100 (independent of total keys)", n)
	}
	if sn.Reads() != uint64(len(keys)) {
		t.Errorf("reads=%d want %d", sn.Reads(), len(keys))
	}
	if ok != 0 {
		t.Errorf("%d keys exposed new values during snapshot", ok)
	}
	sn.Close()

	s2 := store.New()
	for i := 0; i < 50; i++ {
		s2.Put(fmt.Sprintf("k%02d", i), []byte("1"))
	}
	sn2 := snapshot.Take(s2)
	for i := 0; i < 50; i++ {
		s2.Put(fmt.Sprintf("k%02d", i), []byte("2"))
	}
	if n := sn2.RetainedCount(); n != 50 {
		t.Errorf("all-overwritten retains=%d want 50", n)
	}
	sn2.Close()
}

func TestConcurrentAccess(t *testing.T) {
	s := store.New()
	var stop atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				s.Put(fmt.Sprintf("k%d", i%200), []byte{byte(i)})
				if i%50 == 0 {
					s.Delete(fmt.Sprintf("k%d", i%200))
				}
			}
		}(w)
	}
	sn := snapshot.Take(s)
	ks, _ := sn.Keys()
	for _, k := range ks {
		_, _, _ = sn.Get(k)
	}
	sn.Close()
	stop.Store(true)
	wg.Wait()
}
