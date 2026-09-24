package samp

import (
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"testing"

	"ontology/khash"
)

// TestSetRateProbeCount is white-box: it reads the unexported checked
// counter directly (no exported API exposes it) and proves SetRate examines
// O(log m) keys plus only the keys it actually returns.
func TestSetRateProbeCount(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s, err := New(5000, m)
		if err != nil {
			t.Fatal(err)
		}
		evs := make([]Event, m)
		known := make([]string, m)
		for i := range evs {
			known[i] = fmt.Sprintf("k%08x", uint32(i)*2654435761)
			evs[i] = Event{Key: known[i]}
		}
		if _, err := s.Feed(evs); err != nil {
			t.Fatal(err)
		}
		for _, r2 := range []int{5001, 5000} {
			r1 := s.Rate()
			added, removed, err := s.SetRate(r2)
			if err != nil {
				t.Fatal(err)
			}
			var wa, wr []string
			for _, k := range known {
				if !khash.Sampled(k, r1) && khash.Sampled(k, r2) {
					wa = append(wa, k)
				}
				if khash.Sampled(k, r1) && !khash.Sampled(k, r2) {
					wr = append(wr, k)
				}
			}
			if !sorted(added) || !sorted(removed) {
				t.Errorf("m=%d r=%d: result not ordered by (bucket,key)", m, r2)
			}
			if !sameKeys(added, wa) || !sameKeys(removed, wr) {
				t.Errorf("m=%d r=%d: disagrees with naive reference", m, r2)
			}
			if bound := 2*bits.Len(uint(m)) + 4 + len(added) + len(removed); s.checked > bound {
				t.Errorf("m=%d r=%d: checked=%d exceeds bound %d", m, r2, s.checked, bound)
			}
		}
	}
}

func sorted(keys []string) bool {
	for i := 1; i < len(keys); i++ {
		bi, bj := khash.Bucket(keys[i-1]), khash.Bucket(keys[i])
		if bi > bj || bi == bj && keys[i-1] >= keys[i] {
			return false
		}
	}
	return true
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, k := range a {
		set[k] = true
	}
	for _, k := range b {
		if !set[k] {
			return false
		}
	}
	return true
}

func TestErrors(t *testing.T) {
	_, eRate1 := New(-1, 1)
	_, eRate2 := New(10001, 1)
	_, eRate3 := New(0, 0)
	s1, _ := New(0, 10)
	_, _, eRate4 := s1.SetRate(-1)
	_, eEmpty := s1.Feed([]Event{{Key: ""}})
	s2, _ := New(0, 1)
	_, _ = s2.Feed([]Event{{Key: "a"}})
	_, eMany := s2.Feed([]Event{{Key: "b"}})
	for _, c := range []struct {
		name      string
		err, want error
	}{
		{"New rate<0", eRate1, ErrRateRange},
		{"New rate>10000", eRate2, ErrRateRange},
		{"New maxKeys<1", eRate3, ErrRateRange},
		{"SetRate range", eRate4, ErrRateRange},
		{"empty key", eEmpty, ErrEmptyKey},
		{"maxKeys overflow", eMany, ErrTooManyKeys},
	} {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.err, c.want)
		}
	}
	if errors.Is(ErrRateRange, ErrEmptyKey) || errors.Is(ErrEmptyKey, ErrTooManyKeys) || errors.Is(ErrTooManyKeys, ErrRateRange) {
		t.Error("sentinel errors are not distinct")
	}
}

func TestNoTrace(t *testing.T) {
	s, _ := New(2500, 3)
	batch := []Event{{Key: "gnj"}, {Key: "dzv"}, {Key: "gnk"}}
	before, _ := s.Feed(batch)
	_, _, _ = s.SetRate(-1)
	_, _ = s.Feed([]Event{{Key: ""}})
	_, _ = s.Feed([]Event{{Key: "qjy"}})
	after, _ := s.Feed(batch)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected operations changed Feed results")
	}
	if added, removed, _ := s.SetRate(2500); added != nil || removed != nil {
		t.Fatal("rejected SetRate changed the rate")
	}
	if _, err := s.Feed([]Event{{Key: "qjy"}}); !errors.Is(err, ErrTooManyKeys) {
		t.Fatal("rejected key was recorded as known")
	}
}
