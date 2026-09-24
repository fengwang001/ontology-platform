// Command demo runs the built-in RGA CRDT acceptance judgments. It takes no
// arguments and needs no network; exit code is 0 only when every judgment
// passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/doc"
)

var failed bool

func check(ok bool, msg string) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", status, msg)
}

func I(l int, r string) api.ID { return api.ID{Lamport: l, Replica: r} }

func eightSteps() (*api.Doc, []string) {
	d, trace := api.New(), []string{}
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(d.Insert(api.ID{}, I(1, "A"), 'a'))
	trace = append(trace, d.Text())
	must(d.Insert(I(1, "A"), I(2, "A"), 'b'))
	trace = append(trace, d.Text())
	must(d.Insert(I(1, "A"), I(1, "B"), 'x'))
	trace = append(trace, d.Text())
	must(d.Insert(I(2, "A"), I(3, "A"), 'c'))
	trace = append(trace, d.Text())
	must(d.Insert(I(1, "B"), I(2, "B"), 'y'))
	trace = append(trace, d.Text())
	must(d.Delete(I(1, "A")))
	trace = append(trace, d.Text())
	must(d.Insert(I(1, "A"), I(3, "B"), 'z'))
	trace = append(trace, d.Text())
	must(d.Delete(I(2, "A")))
	trace = append(trace, d.Text())
	return d, trace
}

func seedInserts(d *api.Doc, order []int) {
	ops := []func(){
		func() { _ = d.Insert(api.ID{}, I(1, "A"), 'a') },
		func() { _ = d.Insert(I(1, "A"), I(2, "A"), 'b') },
		func() { _ = d.Insert(I(1, "A"), I(1, "B"), 'x') },
		func() { _ = d.Insert(I(2, "A"), I(3, "A"), 'c') },
		func() { _ = d.Insert(I(1, "B"), I(2, "B"), 'y') },
	}
	for _, i := range order {
		ops[i]()
	}
}

func main() {
	d, trace := eightSteps()
	wantTrace := []string{"a", "ab", "abx", "abcx", "abcxy", "bcxy", "zbcxy", "zcxy"}
	ok := len(trace) == len(wantTrace)
	for i := range wantTrace {
		ok = ok && trace[i] == wantTrace[i]
	}
	check(ok && d.Text() == "zcxy", "eight-step trace: "+strings.Join(trace, "|"))
	check(errors.Is(d.SelfCheck(), nil), "SelfCheck four invariants")

	tomb := api.New()
	_ = tomb.Insert(api.ID{}, I(1, "A"), 'a')
	_ = tomb.Insert(I(1, "A"), I(2, "A"), 'b')
	_ = tomb.Delete(I(1, "A"))
	err := tomb.Insert(I(1, "A"), I(9, "Z"), 'z')
	check(err == nil && tomb.Text() == "zb", "insert anchored on tombstoned element")

	p, q := api.New(), api.New()
	seedInserts(p, []int{0, 1, 2, 3, 4})
	seedInserts(q, []int{0, 2, 1, 4, 3})
	tp, tq := p.Text(), q.Text()
	check(tp == "abcxy" && tq == tp && strings.Index(tp, "bc") == 1,
		"causal order stable across delivery orders")

	r := api.New()
	_ = r.Insert(api.ID{}, I(1, "A"), 'a')
	cases := []error{
		r.Insert(api.ID{}, I(1, "A"), 'q'),
		r.Insert(I(9, "A"), I(2, "A"), 'q'),
		r.Insert(api.ID{}, I(3, ""), 'q'),
	}
	wants := []error{api.ErrDuplicateID, api.ErrPrevNotFound, api.ErrInvalidID}
	distinct := true
	for i := range cases {
		distinct = distinct && errors.Is(cases[i], wants[i])
	}
	check(distinct, "three distinct decidable errors")
	check(r.Text() == "a" && r.Insert(I(1, "A"), I(2, "A"), 'b') == nil && r.Text() == "ab",
		"rejected ops leave no trace; doc still usable")

	check(doc.ProbeIsConstant(), "prev-lookup probe constant for m=100..10000")

	const N = 16
	res := make([]string, N)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res[i] = d.Text()
		}(g)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		same = same && res[i] == res[0]
	}
	check(same && res[0] == "zcxy", "16 concurrent readers agree char-for-char")

	if failed {
		os.Exit(1)
	}
}
