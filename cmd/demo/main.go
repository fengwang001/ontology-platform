package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
)

type ev struct {
	k string
	s int64
}

func ch(e ev) api.Change { return api.Change{Key: e.k, Score: e.s} }
func sums(evs []ev, n int) map[string]int64 {
	m := map[string]int64{}
	for _, e := range evs[max(0, len(evs)-n):] {
		m[e.k] += e.s
	}
	return m
}
func brute(evs []ev, n, k int) []api.Entry {
	r := []api.Entry{}
	for key, s := range sums(evs, n) {
		r = append(r, api.Entry{Key: key, Sum: s})
	}
	sort.Slice(r, func(i, j int) bool {
		return r[i].Sum > r[j].Sum || (r[i].Sum == r[j].Sum && r[i].Key < r[j].Key)
	})
	return r[:min(len(r), k)]
}
func winLine(evs []ev, n int) string {
	m := sums(evs, n)
	ks := []string{}
	for key := range m {
		ks = append(ks, key)
	}
	sort.Strings(ks)
	ps := make([]string, len(ks))
	for i, key := range ks {
		ps[i] = key + "=" + strconv.FormatInt(m[key], 10)
	}
	return strings.Join(ps, ",")
}
func topLine(es []api.Entry) string {
	var b strings.Builder
	for _, e := range es {
		fmt.Fprintf(&b, "(%s,%d)", e.Key, e.Sum)
	}
	return b.String()
}
func main() {
	failed := false
	chk := func(n string, ok bool) {
		failed = failed || !ok
		fmt.Println(map[bool]string{true: "OK: ", false: "FAIL: "}[ok] + n)
	}
	seq := []ev{{"a", 6}, {"a", 4}, {"b", 9}, {"c", 8}, {"d", 7}, {"e", 1}, {"f", 8}}
	e, _ := api.New(5, 2)
	var steps []string
	walk := true
	for i, c := range seq {
		walk = e.Feed([]api.Change{ch(c)}) == nil && walk
		got := e.TopK()
		walk = reflect.DeepEqual(got, brute(seq[:i+1], 5, 2)) && walk
		steps = append(steps, fmt.Sprintf("%d:{%s|%s}", i+1, winLine(seq[:i+1], 5), topLine(got)))
	}
	fmt.Println(strings.Join(steps, " "))
	chk("seven steps match brute at each step", walk)
	e6, _ := api.New(5, 2)
	for _, c := range seq[:6] {
		_ = e6.Feed([]api.Change{ch(c)})
	}
	w67 := []api.Entry{{Key: "b", Sum: 9}, {Key: "c", Sum: 8}}
	chk("step6 withdraw/fill; step7 lexicographic tie", reflect.DeepEqual(e6.TopK(), w67) && reflect.DeepEqual(e.TopK(), w67))
	eng, _ := api.New(7, 3)
	var raw []ev
	mixed, st := true, int64(99)
	for i := 0; i < 200; i++ {
		st = (st*1103515245 + 12345) & 0x7fffffff
		c := ev{"k" + strconv.Itoa(int(st%11)), int64(st%15) - 7}
		raw = append(raw, c)
		if eng.Feed([]api.Change{ch(c)}) != nil || !reflect.DeepEqual(eng.TopK(), brute(raw, 7, 3)) {
			mixed = false
		}
	}
	chk("200 mixed/negative events equal brute reference", mixed)
	neg, _ := api.New(3, 2)
	negOK := neg.Feed([]api.Change{{Key: "x", Score: -5}, {Key: "y", Score: 0}, {Key: "x", Score: 5}, {Key: "z", Score: -9}}) == nil
	chk("negative/zero scores handled", negOK && reflect.DeepEqual(neg.TopK(), []api.Entry{{Key: "x", Sum: 5}, {Key: "y", Sum: 0}}))
	_, errN := api.New(0, 1)
	_, errK := api.New(5, 6)
	good, _ := api.New(5, 2)
	errE := good.Feed([]api.Change{{Key: "", Score: 1}})
	chk("three distinct sentinel errors", errors.Is(errN, api.ErrInvalidN) && errors.Is(errK, api.ErrInvalidK) && errors.Is(errE, api.ErrEmptyKey))
	tr, _ := api.New(3, 2)
	_ = tr.Feed([]api.Change{{Key: "a", Score: 10}, {Key: "b", Score: 9}, {Key: "c", Score: 8}})
	before := tr.TopK()
	rj := tr.Feed([]api.Change{{Key: "z", Score: 100}, {Key: "", Score: 1}, {Key: "y", Score: 100}})
	unchanged := reflect.DeepEqual(before, tr.TopK())
	_ = tr.Feed([]api.Change{{Key: "d", Score: 11}})
	chk("rejected batch: no trace, still usable", errors.Is(rj, api.ErrEmptyKey) && unchanged && reflect.DeepEqual(tr.TopK(), []api.Entry{{Key: "d", Sum: 11}, {Key: "b", Sum: 9}}))
	popOK := true
	for _, n := range []int{100, 1000, 10000} {
		big, _ := api.New(n, 10)
		batch := make([]api.Change, n)
		for i := range batch {
			batch[i] = api.Change{Key: "key" + strconv.Itoa(i), Score: int64(i*7 + 1)}
		}
		if big.Feed(batch) != nil || big.SelfCheck() != nil {
			popOK = false
		}
	}
	chk("heap pops O(K) for m=100/1000/10000 (via SelfCheck)", popOK)
	sh, _ := api.New(500, 10)
	batch := make([]api.Change, 500)
	for i := range batch {
		batch[i] = api.Change{Key: "k" + strconv.Itoa(i%47), Score: int64(i%13 - 6)}
	}
	_ = sh.Feed(batch)
	want := sh.TopK()
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				if !reflect.DeepEqual(sh.TopK(), want) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	chk("32 concurrent readers, identical results", !bad.Load())
	if failed {
		fmt.Println("FAIL: demo")
		os.Exit(1)
	}
	fmt.Println("OK: demo all checks passed")
}
