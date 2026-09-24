package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/gtid"
)

const (
	uA = "3e11fa47-71ca-11e1-9e33-c80aa9429562"
	uB = "8a94f357-aab4-11df-86ab-c80aa9429562"
	uC = "00000000-0000-0000-0000-000000000001"
)
// canon is the naive element-wise reference rendered canonically.
func canon(m map[string]map[int64]bool) string {
	var ps []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		ns := slices.Sorted(maps.Keys(m[k]))
		var sg []string
		for i := 0; i < len(ns); {
			j := i + 1
			for j < len(ns) && ns[j] == ns[j-1]+1 {
				j++
			}
			v := strconv.FormatInt(ns[i], 10)
			if j > i+1 {
				v += "-" + strconv.FormatInt(ns[j-1], 10)
			}
			sg, i = append(sg, v), j
		}
		ps = append(ps, k+":"+strings.Join(sg, ":"))
	}
	return strings.Join(ps, ",")
}
func TestRejectedApplyLeavesState(t *testing.T) {
	tr, _ := api.New(100)
	_ = tr.Apply(uC + ":10-20")
	bad := map[string]error{
		" \t": gtid.ErrSyntax, uC + ":1,,2": gtid.ErrSyntax, uC + ":1:": gtid.ErrSyntax,
		uC + "::2": gtid.ErrSyntax, uC + ":1-2-3": gtid.ErrSyntax, uC + ":07": gtid.ErrSyntax,
		"bad-uuid:1": gtid.ErrUUID, uC + ":0": gtid.ErrRange,
		uC + ":9-2": gtid.ErrRange, uC + ":999999999999999999999999": gtid.ErrRange,
	}
	for in, want := range bad {
		snap := tr.Executed()
		if err := tr.Apply(in); !errors.Is(err, want) || tr.Executed() != snap {
			t.Fatalf("Apply %q: %v leaked", in, err)
		}
		if _, err := tr.Missing(in); !errors.Is(err, want) || tr.Executed() != snap {
			t.Fatalf("Missing %q: %v leaked", in, err)
		}
	}
	small, _ := api.New(1)
	_ = small.Apply(uC + ":1")
	if err := small.Apply(uC + ":3"); !errors.Is(err, gtid.ErrTooMany) || small.Executed() != uC+":1" {
		t.Fatalf("overflow leaked: %v %s", err, small.Executed())
	}
	if _, err := api.New(0); !errors.Is(err, gtid.ErrRange) {
		t.Fatalf("New(0): %v", err)
	}
	if len(map[error]bool{gtid.ErrSyntax: true, gtid.ErrUUID: true, gtid.ErrRange: true, gtid.ErrTooMany: true}) != 4 {
		t.Fatal("sentinel errors must be distinct")
	}
}
func TestNaiveRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	us := []string{uA, uB, uC}
	tr, _ := api.New(10000)
	em := map[string]map[int64]bool{uA: {}, uB: {}, uC: {}}
	for range 50 {
		var es []string
		for range 1 + rng.Intn(3) {
			u, a := us[rng.Intn(3)], int64(rng.Intn(197)+1)
			b := a + int64(rng.Intn(4))
			es = append(es, fmt.Sprintf("%s:%d-%d", u, a, b))
			for n := a; n <= b; n++ {
				em[u][n] = true
			}
		}
		rng.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		if err := tr.Apply(strings.Join(es, ",")); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Executed() != canon(em) {
		t.Fatalf("union:\n got %s\nwant %s", tr.Executed(), canon(em))
	}
	var parts []string
	sm := map[string]map[int64]bool{uA: {}, uB: {}, uC: {}}
	for _, u := range us {
		for n := int64(1); n <= 200; n++ {
			if rng.Intn(2) == 0 {
				parts = append(parts, fmt.Sprintf("%s:%d", u, n))
				if !em[u][n] {
					sm[u][n] = true
				}
			}
		}
	}
	gap, err := tr.Missing(strings.Join(parts, ","))
	if err != nil || gap != canon(sm) {
		t.Fatalf("gap=%q %v want %s", gap, err, canon(sm))
	}
}
func TestCanonicalIdempotent(t *testing.T) {
	tr, _ := api.New(100)
	for _, f := range []string{strings.ToUpper(uA) + ":9-12:8:1-3:5-7", uA + ":1-12", uA + ":1-5:6-9:10-12," + uA + ":3:1"} {
		if err := tr.Apply(f); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Executed() != uA+":1-12" {
		t.Fatalf("got %s", tr.Executed())
	}
	rt, _ := api.New(100)
	if err := rt.Apply(tr.Executed()); err != nil || rt.Executed() != uA+":1-12" {
		t.Fatalf("round-trip: %v %s", err, rt.Executed())
	}
	if err := tr.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestDifferenceComplement(t *testing.T) {
	tr, _ := api.New(100)
	if err := tr.Apply(uA + ":1-10:20," + uB + ":5"); err != nil {
		t.Fatal(err)
	}
	src := uA + ":1-25," + uB + ":1-8"
	gap, err := tr.Missing(src)
	if err != nil || gap == "" {
		t.Fatalf("gap=%q %v", gap, err)
	}
	before := tr.Executed()
	if err := tr.Apply(gap); err != nil {
		t.Fatal(err)
	}
	if g, _ := tr.Missing(src); g != "" {
		t.Fatalf("gap not closed: %s", g)
	}
	ref, _ := api.New(100)
	_ = ref.Apply(before)
	_ = ref.Apply(gap)
	if ref.Executed() != tr.Executed() {
		t.Fatal("closing gap changed unrelated state")
	}
}
func TestConcurrentApply(t *testing.T) {
	const N = 64
	tr, _ := api.New(2 * N)
	var writers, readers sync.WaitGroup
	stop := make(chan struct{})
	readers.Add(1)
	go func() { // every concurrent snapshot must itself be canonical
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s := tr.Executed()
				if p, err := gtid.Parse(s); err != nil || p.String() != s {
					t.Errorf("non-canonical snapshot %q: %v", s, err)
				}
			}
		}
	}()
	for g := 0; g < N; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			_ = tr.Apply(fmt.Sprintf("%s:%d-%d", uC, 2*g+1, 2*g+2))
		}(g)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	if tr.Executed() != uC+":1-128" {
		t.Fatalf("got %s", tr.Executed())
	}
}
