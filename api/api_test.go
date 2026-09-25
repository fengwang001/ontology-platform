package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/kv"
)

// TestReadYourWrites: invariant 1 — a session always reads its own writes.
func TestReadYourWrites(t *testing.T) {
	st := api.New()
	ss := []*api.Session{st.Open(), st.Open()}
	wv := []map[string]int64{{}, {}}
	for i := 0; i < 60; i++ {
		me := i % 2
		key := fmt.Sprintf("k%d", i%5)
		v, err := ss[me].Write(key, fmt.Sprintf("v%d", i))
		if err != nil {
			t.Fatal(err)
		}
		wv[me][key] = v
		if i%3 == 0 {
			st.Sync(1 + i%2)
		}
		if _, got, err := ss[me].Read(key); err != nil || got < v {
			t.Fatalf("step %d: read ver %d < writeVer %d (err %v)", i, got, v, err)
		}
	}
	st.Sync(1)
	st.Sync(2)
	for me := 0; me < 2; me++ { // every key this session ever wrote
		for k, w := range wv[me] {
			if _, got, err := ss[me].Read(k); err != nil || got < w {
				t.Fatalf("session %d key %s: ver %d < writeVer %d (err %v)", me, k, got, w, err)
			}
		}
	}
}

// TestNaiveReference: invariant 2 — reads equal the naive reference.
func TestNaiveReference(t *testing.T) {
	st := api.New()
	s := st.Open()
	ref := map[string]kv.Entry{}
	for i := 0; i < 80; i++ {
		key, val := fmt.Sprintf("k%d", i%9), fmt.Sprintf("v%d", i)
		v, err := s.Write(key, val)
		if err != nil {
			t.Fatal(err)
		}
		ref[key] = kv.Entry{Val: val, Ver: v}
		if i%5 == 0 {
			st.Sync(1 + i%2)
		}
		if val2, ver2, err := s.Read(key); err != nil || val2 != val || ver2 != v {
			t.Fatalf("step %d: read (%s,%d,%v), want (%s,%d)", i, val2, ver2, err, val, v)
		}
	}
	if !reflect.DeepEqual(st.View()[0], ref) {
		t.Fatal("primary diverged from naive reference")
	}
}

// TestRejectNoTrace: invariant 4 — rejected ops change nothing.
func TestRejectNoTrace(t *testing.T) {
	st := api.New()
	s := st.Open()
	if _, err := s.Write("k", "a"); err != nil {
		t.Fatal(err)
	}
	closed := st.Open()
	closed.Close()
	before := st.View()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"empty key write", func() error { _, e := s.Write("", "x"); return e }, api.ErrEmptyKey},
		{"empty key read", func() error { _, _, e := s.Read(""); return e }, api.ErrEmptyKey},
		{"sync idx 0", func() error { return st.Sync(0) }, api.ErrBadIndex},
		{"sync idx 3", func() error { return st.Sync(3) }, api.ErrBadIndex},
		{"sync idx -1", func() error { return st.Sync(-1) }, api.ErrBadIndex},
		{"closed write", func() error { _, e := closed.Write("k", "z"); return e }, api.ErrClosed},
		{"closed read", func() error { _, _, e := closed.Read("k"); return e }, api.ErrClosed},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if errors.Is(api.ErrEmptyKey, api.ErrBadIndex) || errors.Is(api.ErrBadIndex, api.ErrClosed) ||
		errors.Is(api.ErrClosed, api.ErrEmptyKey) {
		t.Fatal("fault sentinels are not mutually distinct")
	}
	if !reflect.DeepEqual(before, st.View()) {
		t.Fatal("rejected operations changed replica state")
	}
	if _, got, _ := s.Read("k"); got != 1 { // writeVer intact: still RYW
		t.Fatalf("writeVer changed by rejected ops: read ver %d, want 1", got)
	}
	if v, err := s.Write("k", "b"); err != nil || v != 2 { // still usable
		t.Fatalf("store unusable after rejections: v=%d err=%v", v, err)
	}
}

// TestConcurrentView: concurrent Read/View/SelfCheck; views identical.
func TestConcurrentView(t *testing.T) {
	st := api.New()
	s := st.Open()
	for i := 0; i < 30; i++ {
		if _, err := s.Write(fmt.Sprintf("k%d", i), "v"); err != nil {
			t.Fatal(err)
		}
	}
	st.Sync(1)
	st.Sync(2)
	want := st.View()
	const n = 32
	views := make([][3]map[string]kv.Entry, n)
	errs := make([]error, n)
	scErrs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = s.Read("k7")
			scErrs[i] = st.SelfCheck()
			views[i] = st.View()
		}(i)
	}
	close(start)
	wg.Wait()
	for i, v := range views {
		if errs[i] != nil || scErrs[i] != nil || !reflect.DeepEqual(v, want) {
			t.Fatalf("goroutine %d: read=%v selfcheck=%v viewMatch=%v",
				i, errs[i], scErrs[i], reflect.DeepEqual(v, want))
		}
	}
}
