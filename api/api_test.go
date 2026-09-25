package api_test

import (
	"maps"
	"sync"
	"testing"

	"ontology/api"
)

type op struct {
	key, val string
	ts       int64
	del      bool
}

func setup(t *testing.T, ops []op) *api.Store {
	s := api.New()
	for _, o := range ops {
		err := s.Write(o.key, o.ts, o.val)
		if o.del {
			err = s.Delete(o.key, o.ts)
		}
		if err != nil {
			t.Fatalf("apply %+v: %v", o, err)
		}
	}
	return s
}

func naive(ops []op, key string, T int64) (string, bool) {
	best, val, ok := int64(-1), "", false
	for _, o := range ops {
		if o.key == key && o.ts <= T && o.ts > best {
			best, val, ok = o.ts, o.val, !o.del
		}
	}
	return val, ok
}

var (
	queryTimes = []int64{0, 4, 5, 9, 10, 15, 25, 30, 40, 49, 50, 100}
	scenarios  = []struct {
		name string
		ops  []op
	}{
		{"out-of-order", []op{{"K", "a", 10, false}, {"K", "b", 30, false}, {"K", "c", 20, false}}},
		{"multi", []op{{"K", "a", 10, false}, {"L", "x", 5, false}, {"K", "", 40, true}, {"L", "y", 50, false}}},
	}
)

func TestAsOfMatchesNaive(t *testing.T) {
	for _, sc := range scenarios {
		s := setup(t, sc.ops)
		for _, T := range queryTimes {
			for _, key := range []string{"K", "L", "absent"} {
				wantV, wantOK := naive(sc.ops, key, T)
				if gotV, gotOK := s.AsOf(key, T); gotV != wantV || gotOK != wantOK {
					t.Errorf("%s: AsOf(%s,%d)=(%q,%v), want (%q,%v)", sc.name, key, T, gotV, gotOK, wantV, wantOK)
				}
			}
		}
	}
}

func TestViewAsOfMatchesReplay(t *testing.T) {
	for _, sc := range scenarios {
		s := setup(t, sc.ops)
		for _, T := range queryTimes {
			replay := map[string]string{}
			for _, key := range []string{"K", "L"} {
				if v, ok := naive(sc.ops, key, T); ok {
					replay[key] = v
				}
			}
			if view := s.ViewAsOf(T); !maps.Equal(view, replay) {
				t.Errorf("%s: ViewAsOf(%d)=%v, want %v", sc.name, T, view, replay)
			}
		}
	}
}

func TestImmutability(t *testing.T) {
	s := setup(t, []op{{"K", "a", 10, false}, {"K", "b", 30, false}, {"K", "c", 20, false}, {"K", "", 40, true}})
	before := map[int64]string{}
	for _, T := range queryTimes {
		before[T], _ = s.AsOf("K", T)
	}
	if s.Write("K", 200, "d") != nil || s.Write("K", 150, "e") != nil {
		t.Fatal("later writes failed")
	}
	for _, T := range queryTimes {
		if got, _ := s.AsOf("K", T); got != before[T] {
			t.Errorf("AsOf(K,%d): %q -> %q after later writes", T, before[T], got)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	bads := []struct {
		del      bool
		key, val string
		ts       int64
		want     error
	}{
		{false, "", "v", 1, api.ErrEmptyKey},
		{false, "K", "v", -1, api.ErrNegativeTS},
		{false, "K", "", 1, api.ErrEmptyValue},
		{true, "", "", 1, api.ErrEmptyKey},
		{true, "K", "", -1, api.ErrNegativeTS},
	}
	if api.ErrEmptyKey == api.ErrNegativeTS || api.ErrEmptyKey == api.ErrEmptyValue || api.ErrNegativeTS == api.ErrEmptyValue {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	for _, b := range bads {
		s := setup(t, []op{{"K", "a", 10, false}})
		err := s.Write(b.key, b.ts, b.val)
		if b.del {
			err = s.Delete(b.key, b.ts)
		}
		if err != b.want {
			t.Errorf("%+v: got %v, want %v", b, err, b.want)
		}
		if got, _ := s.AsOf("K", 100); got != "a" || s.Write("K", 20, "b") != nil {
			t.Errorf("%+v: rejection left trace or broke store", b)
		}
	}
}

func TestConcurrentAsOfConsistent(t *testing.T) {
	s := setup(t, []op{{"K", "a", 10, false}, {"K", "b", 30, false}, {"K", "c", 20, false}, {"K", "", 40, true}})
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if v, ok := s.AsOf("K", 30); v != "b" || !ok {
				t.Errorf("got (%q,%v), want (\"b\",true)", v, ok)
			}
			if err := s.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
}
