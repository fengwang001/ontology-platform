// Command demo runs the ontology top-K self-contained checks and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/topk"
)

var failed bool

func ck(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	if detail != "" {
		name += ": " + detail
	}
	fmt.Printf("%s %s\n", tag, name)
}

func tops(v *api.View, key string) string {
	xs := v.TopK(key)
	ss := make([]string, len(xs))
	for i, x := range xs {
		ss[i] = x.ItemID
	}
	return "[" + strings.Join(ss, " ") + "]"
}

func main() {
	v, err := api.New(2)
	if err != nil {
		fmt.Println("FAIL new:", err)
		os.Exit(1)
	}
	// Seven steps from NOTES.md; record the list after EVERY step.
	adds := []struct {
		id string
		sc int
	}{{"a", 10}, {"b", 20}, {"c", 15}, {"d", 15}, {"e", 20}}
	var seq []string
	for _, a := range adds {
		_ = v.Add("g", a.id, a.sc)
		seq = append(seq, tops(v, "g"))
	}
	ck("step4 tie: c stays in-list over d", seq[3] == "[b c]", seq[3])
	_ = v.Remove("g", "b")
	seq = append(seq, tops(v, "g"))
	ck("step6 promote fills c", tops(v, "g") == "[e c]", tops(v, "g"))
	_ = v.Remove("g", "e")
	seq = append(seq, tops(v, "g"))
	want := []string{"[a]", "[b a]", "[b c]", "[b c]", "[b e]", "[e c]", "[c d]"}
	ck("seven-step lists", reflect.DeepEqual(seq, want), strings.Join(seq, " "))

	before := tops(v, "g")
	rejected := errors.Is(v.Add("g", "c", 999), api.ErrDuplicate) &&
		errors.Is(v.Remove("g", "ghost"), api.ErrNotFound)
	ck("dup/missing rejected, no trace", rejected && tops(v, "g") == before, before)

	_, badK := api.New(0)
	four := errors.Is(badK, api.ErrInvalidK) &&
		errors.Is(v.Add("", "x", 1), api.ErrEmptyArgument) &&
		errors.Is(v.Add("g", "c", 1), api.ErrDuplicate) &&
		errors.Is(v.Remove("g", "ghost"), api.ErrNotFound)
	distinct := api.ErrInvalidK != api.ErrEmptyArgument && api.ErrInvalidK != api.ErrDuplicate &&
		api.ErrInvalidK != api.ErrNotFound && api.ErrEmptyArgument != api.ErrDuplicate &&
		api.ErrEmptyArgument != api.ErrNotFound && api.ErrDuplicate != api.ErrNotFound
	ck("four fault classes distinct & decidable", four && distinct, "")

	ck("admission O(1) across m=100..10000", topk.AdmissionCheckBounded() == nil, "")

	w, _ := api.New(10)
	for i := 0; i < 200; i++ {
		_ = w.Add("g", fmt.Sprintf("i%03d", i), i)
	}
	base := w.TopK("g")
	const n = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	bad := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 100; r++ {
				if !reflect.DeepEqual(w.TopK("g"), base) {
					bad <- true
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	ck("concurrent TopK identical, 64 readers", len(bad) == 0, "")

	ck("SelfCheck four invariants", v.SelfCheck() == nil, "")

	if failed {
		os.Exit(1)
	}
}
