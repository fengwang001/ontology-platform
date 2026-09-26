package delta_test

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/api"
	"ontology/delta"
	"ontology/replica"
)

func TestApplyChange(t *testing.T) {
	cases := []struct {
		name string
		init map[string]int
		c    delta.Change
		want map[string]int
		err  error
	}{
		{"set", map[string]int{}, delta.Set("a", 1), map[string]int{"a": 1}, nil},
		{"overwrite", map[string]int{"a": 1}, delta.Set("a", 9), map[string]int{"a": 9}, nil},
		{"del", map[string]int{"a": 1}, delta.Del("a"), map[string]int{}, nil},
		{"del absent", map[string]int{"a": 1}, delta.Del("b"), map[string]int{"a": 1}, nil},
		{"empty set", map[string]int{}, delta.Set("", 1), map[string]int{}, delta.ErrEmptyKey},
		{"empty del", map[string]int{}, delta.Del(""), map[string]int{}, delta.ErrEmptyKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := delta.ApplyChange(tc.init, tc.c)
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(tc.init, tc.want) {
				t.Fatalf("%s: err=%v state=%v", tc.name, err, tc.init)
			}
		})
	}
}

func TestAPIFacade(t *testing.T) {
	a := api.New()
	if a.Version() != 0 || len(a.State()) != 0 || a.SelfCheck() != nil {
		t.Fatal("bad new api")
	}
	d := delta.Delta{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 1)}}
	if e := a.Apply(d); e != nil || a.Version() != 1 || a.State()["a"] != 1 {
		t.Fatal("apply via facade failed")
	}
	bad := []struct {
		d    delta.Delta
		want error
	}{
		{delta.Delta{From: 9, To: 10}, replica.ErrGap},
		{delta.Delta{From: 0, To: 0}, replica.ErrInvalidRange},
		{delta.Delta{From: -1, To: 1}, replica.ErrNegativeVersion},
	}
	for _, c := range bad {
		if !errors.Is(a.Apply(c.d), c.want) {
			t.Fatal("facade sentinel mismatch")
		}
	}
}

// replay is the naive reference: from v0/empty apply only when From==version.
func replay(ds []delta.Delta) (int, map[string]int) {
	v, st := 0, map[string]int{}
	for _, d := range ds {
		if d.From != v {
			continue
		}
		for _, c := range d.Changes {
			_ = delta.ApplyChange(st, c)
		}
		v = d.To
	}
	return v, st
}

// randStream builds a random in-order/duplicate/gap arrival stream.
func randStream(seed int64) []delta.Delta {
	rng := rand.New(rand.NewSource(seed))
	var ds []delta.Delta
	for v := 0; v < 30; {
		switch x := rng.Intn(3); {
		case x == 0 && v > 0:
			f := rng.Intn(v)
			ds = append(ds, delta.Delta{From: f, To: f + 1})
		case x == 1:
			ds = append(ds, delta.Delta{From: v + 2, To: v + 3})
		default:
			cs := []delta.Change{delta.Set(string(rune('a'+rng.Intn(8))), rng.Intn(5))}
			if rng.Intn(2) == 0 {
				cs = append(cs, delta.Del(string(rune('a'+rng.Intn(8)))))
			}
			ds = append(ds, delta.Delta{From: v, To: v + 1, Changes: cs})
			v++
		}
	}
	return ds
}

func TestReplayEquivalence(t *testing.T) {
	for seed := int64(0); seed < 10; seed++ {
		ds := randStream(seed)
		r := replica.New()
		for _, d := range ds {
			if e := r.Apply(d); e != nil && !errors.Is(e, replica.ErrGap) {
				t.Fatal(e)
			}
		}
		wv, ws := replay(ds)
		if r.Version() != wv || !reflect.DeepEqual(r.State(), ws) {
			t.Fatalf("seed %d mismatch", seed)
		}
	}
}

func TestDuplicateIdempotent(t *testing.T) {
	r := replica.New()
	ds := []delta.Delta{
		{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 1)}},
		{From: 1, To: 2, Changes: []delta.Change{delta.Set("b", 2)}},
		{From: 2, To: 3, Changes: []delta.Change{delta.Set("c", 3), delta.Del("b")}},
		{From: 3, To: 4, Changes: []delta.Change{delta.Set("d", 4)}},
		{From: 0, To: 1, Changes: []delta.Change{delta.Set("a", 100)}},
	}
	for _, d := range ds {
		if e := r.Apply(d); e != nil {
			t.Fatal(e)
		}
	}
	want := map[string]int{"a": 1, "c": 3, "d": 4}
	if r.Version() != 4 || !reflect.DeepEqual(r.State(), want) {
		t.Fatalf("after D5: %d %v", r.Version(), r.State())
	}
	for _, d := range ds {
		if e := r.Apply(d); e != nil || r.Version() != 4 || !reflect.DeepEqual(r.State(), want) {
			t.Fatal("duplicate left a trace")
		}
	}
}
