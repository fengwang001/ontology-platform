package main

import (
	"fmt"
	"maps"
	"math/rand"
	"ontology/api"
	"ontology/pcol"
	"os"
	"sync"
)

var fails int
var S, N = pcol.Str, pcol.Null

type vm = map[string]pcol.Value

func ok(name string, cond bool) {
	fails += map[bool]int{false: 1}[cond]
	fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[cond] + name)
}
func rv(r *rand.Rand) pcol.Value {
	return []pcol.Value{N(), S(""), S(fmt.Sprint(r.Intn(100))), S(fmt.Sprint(r.Intn(100)))}[r.Intn(4)]
}
func genBatch(r *rand.Rand, cols, keys []string, naive map[string]vm) []pcol.Event {
	var batch []pcol.Event
	for i := 0; i < 1+r.Intn(8); i++ {
		k, force := keys[r.Intn(len(keys))], cols[r.Intn(len(cols))]
		row, exists := naive[k]
		e := pcol.Event{Kind: pcol.Update, Key: k, Set: vm{}, Before: vm{}}
		for _, c := range cols {
			if !exists || c == force || r.Intn(2) == 1 {
				e.Before[c], e.Set[c] = row[c], rv(r)
			}
		}
		if !exists {
			e.Kind, e.Before, row = pcol.Insert, nil, vm{}
		}
		batch = append(batch, e)
		for c, v := range e.Set {
			row[c] = v
		}
		naive[k] = row
	}
	return batch
}
func rowsMatch(tb *api.Table, naive map[string]vm) bool {
	for k, want := range naive {
		if got, ok := tb.Row(k); !ok || !maps.Equal(got, want) {
			return false
		}
	}
	return true
}
func checkSixStep() bool {
	evs := []pcol.Event{
		pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": S("2")}, Before: vm{"a": S("1")}}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"b": N()}, Before: vm{"b": S("x")}}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": S("3"), "c": S("z")}, Before: vm{"a": S("2"), "c": N()}}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"b": S("x")}, Before: vm{"b": N()}}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"c": N()}, Before: vm{"c": S("z")}}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": N()}, Before: vm{"a": S("3")}},
	}
	want := []struct{ set, before vm }{
		{vm{"a": S("2")}, vm{"a": S("1")}}, {vm{"a": S("2"), "b": N()}, vm{"a": S("1"), "b": S("x")}}, {vm{"a": S("3"), "b": N(), "c": S("z")}, vm{"a": S("1"), "b": S("x"), "c": N()}}, {vm{"a": S("3"), "c": S("z")}, vm{"a": S("1"), "c": N()}}, {vm{"a": S("3")}, vm{"a": S("1")}}, {vm{"a": N()}, vm{"a": S("1")}},
	}
	mg, acc := pcol.Merger{}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{}, Before: vm{}}
	for i, e := range evs {
		acc = mg.Merge(acc, e)
		if !maps.Equal(acc.Set, want[i].set) || !maps.Equal(acc.Before, want[i].before) {
			return false
		}
	}
	tb, _ := api.New([]string{"a", "b", "c"})
	tb.Apply([]pcol.Event{{Kind: pcol.Insert, Key: "k", Set: vm{"a": S("1"), "b": S("x"), "c": N()}}})
	out, err := tb.Apply(evs)
	return err == nil && len(out) == 1 && maps.Equal(out[0].Set, want[5].set) && maps.Equal(out[0].Before, want[5].before) && rowsMatch(tb, map[string]vm{"k": vm{"a": N(), "b": S("x"), "c": N()}})
}
func checkSmall() {
	tb, _ := api.New([]string{"a", "b"})
	tb.Apply([]pcol.Event{{Kind: pcol.Insert, Key: "k", Set: vm{"a": S(""), "b": N()}}})
	tb.Apply([]pcol.Event{pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": S("z")}, Before: vm{"a": S("")}}})
	ok("2 absent vs NULL vs empty-string", rowsMatch(tb, map[string]vm{"k": vm{"a": S("z"), "b": N()}}) && S("") != N())
	out, _ := tb.Apply([]pcol.Event{{Kind: pcol.Insert, Key: "j", Set: vm{"a": S("1"), "b": S("x")}}, pcol.Event{Kind: pcol.Update, Key: "j", Set: vm{"b": N()}, Before: vm{"b": S("x")}}})
	ok("3 insert+update merges to insert", len(out) == 1 && out[0].Kind == pcol.Insert && maps.Equal(out[0].Set, vm{"a": S("1"), "b": N()}))
	s := []error{pcol.ErrBadColumn, pcol.ErrNoSuchKey, pcol.ErrKeyExists, pcol.ErrBeforeMismatch}
	ok4 := s[0] != s[1] && s[0] != s[2] && s[0] != s[3] && s[1] != s[2] && s[1] != s[3] && s[2] != s[3]
	batches := [][]pcol.Event{{pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"zz": S("1")}, Before: vm{"zz": S("1")}}}, {pcol.Event{Kind: pcol.Update, Key: "ghost", Set: vm{"a": S("1")}, Before: vm{"a": S("1")}}}, {{Kind: pcol.Insert, Key: "k", Set: vm{"a": S("1"), "b": N()}}}, {pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": S("2")}, Before: vm{"a": S("9")}}}}
	for i, want := range s {
		if _, err := tb.Apply(batches[i]); err != want {
			ok4 = false
		}
	}
	ok("4 four decidable distinct errors", ok4)
	_, err := tb.Apply([]pcol.Event{{Kind: pcol.Insert, Key: "x", Set: vm{"a": S("2"), "b": N()}}, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"a": S("3")}, Before: vm{"a": S("9")}}})
	_, xThere := tb.Row("x")
	ok("5 rejected batch leaves no trace", err == pcol.ErrBeforeMismatch && !xThere && rowsMatch(tb, map[string]vm{"k": vm{"a": S("z"), "b": N()}, "j": vm{"a": S("1"), "b": N()}}))
}
func checkRandom() bool {
	cols, keys := []string{"c0", "c1", "c2", "c3"}, []string{"k0", "k1", "k2", "k3", "k4"}
	for seed := int64(0); seed < 20; seed++ {
		r, naive := rand.New(rand.NewSource(seed)), map[string]vm{}
		tb, _ := api.New(cols)
		for b := 0; b < 5; b++ {
			if _, err := tb.Apply(genBatch(r, cols, keys, naive)); err != nil {
				return false
			}
		}
		if !rowsMatch(tb, naive) {
			return false
		}
	}
	return true
}
func checkLargeM() bool {
	const m = 10000
	cols, set := make([]string, m), vm{}
	u1 := pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{}, Before: vm{}}
	for i := range cols {
		cols[i] = fmt.Sprint("c", i)
		set[cols[i]], u1.Set[cols[i]], u1.Before[cols[i]] = S("0"), S("1"), S("0")
	}
	tb, _ := api.New(cols)
	tb.Apply([]pcol.Event{{Kind: pcol.Insert, Key: "k", Set: set}})
	out, err := tb.Apply([]pcol.Event{u1, pcol.Event{Kind: pcol.Update, Key: "k", Set: vm{"c0": S("2")}, Before: vm{"c0": S("1")}}})
	return err == nil && len(out) == 1 && len(out[0].Set) == m && out[0].Set["c0"] == S("2")
}
func checkConcurrent() bool {
	tb, _ := api.New([]string{"a", "b"})
	var wg sync.WaitGroup
	naives := []map[string]vm{{}, {}, {}, {}, {}, {}, {}, {}}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := 0; b < 6; b++ {
				tb.Apply(genBatch(rand.New(rand.NewSource(int64(g*8+b))), []string{"a", "b"}, []string{fmt.Sprint("g", g)}, naives[g]))
			}
		}()
	}
	wg.Wait()
	all := map[string]vm{}
	for g := 0; g < 8; g++ {
		all[fmt.Sprint("g", g)] = naives[g][fmt.Sprint("g", g)]
	}
	return rowsMatch(tb, all)
}
func main() {
	ok("1 six-step merge + final output", checkSixStep())
	checkSmall()
	ok("6 random batches match naive reference", checkRandom())
	ok("7 large-m merge result correct", checkLargeM())
	ok("8 concurrent distinct-key updates", checkConcurrent())
	os.Exit(map[bool]int{true: 1}[fails > 0])
}
