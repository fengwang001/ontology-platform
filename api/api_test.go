package api_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"

	"ontology/api"
	"ontology/compact"
	"ontology/rec"
)

var ten = []rec.Rec{
	{Key: "a", Value: 1, TS: 1}, {Key: "b", Value: 10, TS: 2}, {Key: "a", Value: 2, TS: 4}, {Key: "c", Value: 5, TS: 3}, {Key: "b", Value: 0, TS: 5, Del: true},
	{Key: "a", Value: 0, TS: 6, Del: true}, {Key: "c", Value: 0, TS: 7, Del: true}, {Key: "a", Value: 3, TS: 8}, {Key: "d", Value: 7, TS: 6}, {Key: "d", Value: 9, TS: 2},
}

func newFed(t *testing.T, ret int64, rs []rec.Rec) *api.API {
	t.Helper()
	a, err := api.New(ret)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Feed(rs); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	return a
}

func TestNaiveEquivalence(t *testing.T) {
	cases := []struct {
		name        string
		ret, lo, hi int64
		rs          []rec.Rec
	}{
		{"ten R5", 5, 0, 10, ten}, {"ten R0", 0, 0, 10, ten},
		{"ten [3,8)", 5, 3, 8, ten}, {"ten [-5,9)", 5, -5, 9, ten}, {"empty", 5, 0, 10, nil},
		{"tomb at hi R0", 0, 0, 20, []rec.Rec{{Key: "k", TS: 10, Del: true}, {Key: "k", Value: 9, TS: 2}}},
	}
	for _, tc := range cases {
		a := newFed(t, tc.ret, tc.rs)
		got, err := a.Compact(tc.lo, tc.hi)
		if err != nil {
			t.Fatalf("%s Compact: %v", tc.name, err)
		}
		if want := compact.Naive(a.View(), tc.lo, tc.hi, tc.ret); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %v want %v", tc.name, got, want)
		}
	}
	for _, m := range []int{10, 100, 1000} {
		rng := rand.New(rand.NewPCG(uint64(m), 7))
		ret := int64(rng.IntN(10))
		rs := make([]rec.Rec, m)
		for i := range rs {
			rs[i] = rec.Rec{Key: fmt.Sprintf("key%d", i%37), Value: i, TS: int64(rng.IntN(1000)), Del: rng.IntN(3) == 0}
		}
		rng.Shuffle(m, func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
		a := newFed(t, ret, rs)
		for _, w := range [][2]int64{{0, 1000}, {0, 100}, {250, 750}, {-10, 50}} {
			got, _ := a.Compact(w[0], w[1])
			if want := compact.Naive(a.View(), w[0], w[1], ret); !reflect.DeepEqual(got, want) {
				t.Fatalf("m=%d %v got %v want %v", m, w, got, want)
			}
		}
	}
}

func TestNoDuplicateKeys(t *testing.T) {
	check := func(t *testing.T, a *api.API) {
		got, _ := a.Compact(0, 1<<30)
		seen := map[string]bool{}
		for i, r := range got {
			if seen[r.Key] || (i > 0 && got[i-1].Key >= r.Key) {
				t.Fatalf("dup/unsorted at %q index %d", r.Key, i)
			}
			seen[r.Key] = true
		}
	}
	check(t, newFed(t, 1<<20, ten))
	for _, m := range []int{50, 500, 5000} {
		rs := make([]rec.Rec, m)
		for i := range rs {
			rs[i] = rec.Rec{Key: fmt.Sprintf("k%04d", i%64), TS: int64(i)}
		}
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) { check(t, newFed(t, 1<<20, rs)) })
	}
}

func TestNoResurrection(t *testing.T) {
	cases := []struct {
		name    string
		ret, hi int64
		rs      []rec.Rec
		key     string
		absent  bool
		wantTS  int64
		wantDel bool
	}{
		{"b dropped hides put", 5, 10, ten, "b", true, 0, false},
		{"c kept tomb", 5, 10, ten, "c", false, 7, true},
		{"a newer put wins", 5, 10, ten, "a", false, 8, false},
		{"R0 tomb dropped", 0, 10, []rec.Rec{{Key: "k", Value: 5, TS: 1}, {Key: "k", Del: true, TS: 9}}, "k", true, 0, false},
	}
	for _, tc := range cases {
		got, _ := newFed(t, tc.ret, tc.rs).Compact(0, tc.hi)
		var hit *rec.Rec
		for i := range got {
			if got[i].Key == tc.key {
				hit = &got[i]
			}
		}
		if tc.absent && hit != nil {
			t.Fatalf("%s resurrected %+v", tc.name, *hit)
		}
		if !tc.absent && (hit == nil || hit.TS != tc.wantTS || hit.Del != tc.wantDel) {
			t.Fatalf("%s survivor %+v want TS=%d Del=%v", tc.name, hit, tc.wantTS, tc.wantDel)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a := newFed(t, 5, ten)
	before := a.View()
	beforeOut, _ := a.Compact(0, 10)
	cases := []struct {
		name string
		k    func(*api.API) error
		want error
	}{
		{"window", func(a *api.API) error { _, e := a.Compact(5, 2); return e }, compact.ErrBadWindow},
		{"empty key", func(a *api.API) error { return a.Feed([]rec.Rec{{Key: "", TS: 1}}) }, api.ErrEmptyKey},
		{"negative ts", func(a *api.API) error { return a.Feed([]rec.Rec{{Key: "z", TS: -1}}) }, api.ErrNegativeTS},
		{"mixed batch", func(a *api.API) error { return a.Feed([]rec.Rec{{Key: "ok", TS: 1}, {Key: "x", TS: -2}}) }, api.ErrNegativeTS},
	}
	for _, tc := range cases {
		if !errors.Is(tc.k(a), tc.want) || !reflect.DeepEqual(a.View(), before) {
			t.Fatalf("rejection/state for %s", tc.name)
		}
		if after, _ := a.Compact(0, 10); !reflect.DeepEqual(after, beforeOut) {
			t.Fatalf("result changed after rejection")
		}
	}
	_, err := api.New(-1)
	if !errors.Is(err, api.ErrBadRetention) || errors.Is(err, compact.ErrBadWindow) ||
		errors.Is(err, api.ErrEmptyKey) || errors.Is(err, api.ErrNegativeTS) {
		t.Fatalf("retention error wrong/not distinct: %v", err)
	}
}
