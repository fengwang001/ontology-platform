package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"ontology/api"
	"reflect"
	"strings"
	"testing"
)

func sp(s string) *string { return &s }

func must(t *testing.T, ok bool, f string, a ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(f, a...)
	}
}

var cfg = map[string]map[string]string{"users": {"email": "email", "phone": "phone"}, "orders": {"buyer_email": "email"}}

var seven = []api.Event{
	{Table: "users", Op: 'I', After: map[string]*string{"email": sp("a"), "phone": sp("111")}}, {Table: "orders", Op: 'I', After: map[string]*string{"buyer_email": sp("b"), "amount": sp("9")}}, {Table: "orders", Op: 'U', Before: map[string]*string{"buyer_email": sp("b"), "amount": sp("9")}, After: map[string]*string{"buyer_email": sp("b"), "amount": sp("12")}}, {Table: "users", Op: 'U', Before: map[string]*string{"email": sp("a"), "phone": sp("111")}, After: map[string]*string{"email": sp("c"), "phone": nil}}, {Table: "orders", Op: 'U', Before: map[string]*string{"buyer_email": sp("b")}, After: map[string]*string{"buyer_email": sp("")}}, {Table: "users", Op: 'I', After: map[string]*string{"email": sp("d"), "phone": sp("222")}}, {Table: "users", Op: 'I', After: map[string]*string{"email": sp("e"), "phone": nil}},
}

func render(e api.Event) string {
	f := func(m map[string]*string) (s string) {
		for _, k := range []string{"amount", "buyer_email", "email", "phone"} {
			if v, ok := m[k]; ok {
				x := "NULL"
				if v != nil {
					x = *v
				}
				s += " " + k + "=" + x
			}
		}
		return "{" + strings.TrimSpace(s) + "}"
	}
	if e.Before != nil {
		return "B" + f(e.Before) + "A" + f(e.After)
	}
	return "A" + f(e.After)
}

func TestSevenEvents(t *testing.T) {
	mk, _ := api.New(cfg, 6)
	want := strings.Split("A{email=email#1 phone=phone#1}|A{amount=9 buyer_email=email#2}|B{amount=9 buyer_email=email#2}A{amount=12 buyer_email=email#2}|B{email=email#1 phone=phone#1}A{email=email#3 phone=NULL}|B{buyer_email=email#2}A{buyer_email=email#4}||A{email=email#5 phone=NULL}", "|")
	for i, ev := range seven {
		out, err := mk.Mask(ev)
		if i == 5 {
			must(t, errors.Is(err, api.ErrTokenLimit) && reflect.DeepEqual(out, api.Event{}), "step6 %v", err)
		} else {
			must(t, err == nil && render(out) == want[i], "step%d %s", i+1, render(out))
		}
		must(t, reflect.DeepEqual(api.Event{Table: ev.Table, Op: ev.Op, Before: maps.Clone(ev.Before), After: maps.Clone(ev.After)}, ev), "step%d mutated input", i+1)
		must(t, mk.Size() == []int{2, 3, 3, 4, 5, 5, 6}[i], "step%d size %d", i+1, mk.Size())
	}
	m2, _ := api.New(cfg, 6)
	must(t, m2.SelfCheck() == nil, "SelfCheck failed")
}

// TestNaiveReference compares random insert events with independent
// first-seen numbering (sorted-column order, two domains); Before/After
// sharing, NULL and empty-string numbering are pinned by TestSevenEvents.
func TestNaiveReference(t *testing.T) {
	mk, _ := api.New(cfg, 1e6)
	first, nxt, r := map[string]int{}, map[string]int{}, rand.New(rand.NewSource(1))
	es, ps := []string{"a", "b", "c", "x"}, []string{"1", "2", "3"}
	fill := func() map[string]*string {
		return map[string]*string{"email": sp(es[r.Intn(4)]), "phone": sp(ps[r.Intn(3)])}
	}
	chk := func(in, out map[string]*string) {
		for _, k := range []string{"email", "phone"} {
			d := cfg["users"][k]
			key := d + "|" + *in[k]
			if first[key] == 0 {
				nxt[d]++
				first[key] = nxt[d]
			}
			must(t, *out[k] == fmt.Sprintf("%s#%d", d, first[key]), "%s=%s", k, *out[k])
		}
	}
	for i := 0; i < 200; i++ {
		in := fill()
		out, err := mk.Mask(api.Event{Table: "users", Op: 'I', After: in})
		must(t, err == nil, "%v", err)
		chk(in, out.After)
	}
	o, _ := mk.Mask(api.Event{Table: "orders", Op: 'I', After: map[string]*string{"buyer_email": sp("ZZ")}})
	w, _ := mk.Mask(api.Event{Table: "users", Op: 'I', After: map[string]*string{"email": sp("ZZ"), "phone": sp("ZZ")}})
	must(t, *o.After["buyer_email"] == *w.After["email"], "same domain must share across tables")
	must(t, *w.After["email"] != *w.After["phone"], "different domains must differ")
}

func TestRejectedLeavesState(t *testing.T) {
	_, e0 := api.New(cfg, 0)
	_, e1 := api.New(map[string]map[string]string{"t": {"c": ""}}, 1)
	mk, _ := api.New(cfg, 6)
	for i := 0; i < 5; i++ {
		mk.Mask(seven[i])
	}
	em := map[string]*string{}
	bad := []api.Event{{Table: "users", Op: 'X'}, {Table: "users", Op: 'I', Before: em}, {Table: "users", Op: 'I'}, {Table: "users", Op: 'D', After: em}, {Table: "users", Op: 'D'}, {Table: "users", Op: 'U'}, {Table: "users", Op: 'U', Before: map[string]*string{"a": sp("1")}, After: map[string]*string{"b": sp("2")}}}
	must(t, errors.Is(e0, api.ErrBadConfig) && errors.Is(e1, api.ErrBadConfig), "bad config")
	_, eu := mk.Mask(api.Event{Table: "x", Op: 'I', After: em})
	must(t, errors.Is(eu, api.ErrUnknownTable), "unknown table")
	for i, ev := range bad {
		_, e := mk.Mask(ev)
		must(t, errors.Is(e, api.ErrEventShape) && mk.Size() == 5, "shape %d: %v", i, e)
	}
	_, el := mk.Mask(seven[5])
	must(t, errors.Is(el, api.ErrTokenLimit) && mk.Size() == 5, "token limit")
	o, e := mk.Mask(seven[6])
	must(t, e == nil && *o.After["email"] == "email#5" && o.After["phone"] == nil, "unusable after rejects: %v", e)
	must(t, api.ErrBadConfig != api.ErrUnknownTable && api.ErrUnknownTable != api.ErrEventShape && api.ErrEventShape != api.ErrTokenLimit, "sentinels must differ")
}

// TestConcurrency hammers one Masker from 16 goroutines over a shared,
// duplicated value pool, with concurrent Size calls; no sleeps anywhere.
func TestConcurrency(t *testing.T) {
	mk, _ := api.New(map[string]map[string]string{"t": {"c": "d"}}, 1e6)
	res := make(chan map[string]string, 16)
	for g := 0; g < 16; g++ {
		go func(g int) {
			r, loc := rand.New(rand.NewSource(int64(g)+1)), map[string]string{}
			for i := 0; i < 200; i++ {
				v := fmt.Sprintf("v%02d", r.Intn(40))
				o, _ := mk.Mask(api.Event{Table: "t", Op: 'I', After: map[string]*string{"c": &v}})
				loc[v] = *o.After["c"]
				_ = mk.Size()
			}
			res <- loc
		}(g)
	}
	seen := map[string]string{}
	for g := 0; g < 16; g++ {
		for v, tok := range <-res {
			if old, ok := seen[v]; old != tok && ok {
				t.Fatalf("value %q got %q and %q", v, old, tok)
			}
			seen[v] = tok
		}
	}
	nums := map[int]bool{}
	for _, tok := range seen {
		var n int
		fmt.Sscanf(tok, "d#%d", &n)
		nums[n] = true
	}
	for n := 1; n <= len(seen); n++ {
		must(t, nums[n], "numbering hole or duplicate at d#%d", n)
	}
}
