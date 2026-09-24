package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/ptr"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println(name, "FAIL")
		return
	}
	fmt.Println(name, "OK")
}

func j(s string) any { var v any; _ = json.Unmarshal([]byte(s), &v); return v }

func rdoc(r *rand.Rand, d int) any {
	switch r.Intn(4) {
	case 0:
		return float64(r.Intn(100))
	case 1:
		return r.Intn(2) == 0
	case 2:
		if d <= 0 {
			return nil
		}
		m := map[string]any{}
		for i := r.Intn(4); i >= 0; i-- {
			m[string(rune('a'+r.Intn(4)))] = rdoc(r, d-1)
		}
		return m
	default:
		if d <= 0 {
			return "x"
		}
		s := []any{}
		for i := r.Intn(3); i >= 0; i-- {
			s = append(s, rdoc(r, d-1))
		}
		return s
	}
}

func main() {
	h := api.New(100)
	A := j(`{"a/b":1,"arr":[5,6,7,8],"m~n":{"x":1,"y":2},"z":null,"~1":true}`)
	B := j(`{"a/b":2,"arr":[5],"k":{"q":1},"m~n":{"y":3,"w":[]},"n":null,"~1":false}`)
	want := []string{"replace /a~1b", "replace /arr", "add /k", "add /m~0n/w", "remove /m~0n/x",
		"replace /m~0n/y", "add /n", "remove /z", "replace /~01"}
	ops, err := h.Diff(A, B)
	got := []string{}
	for _, o := range ops {
		got = append(got, o.Op+" "+o.Path)
	}
	for i := 0; i < 9; i += 3 {
		line, bad := "", err != nil || len(got) != 9
		for k := i; k < i+3; k++ {
			mark := "OK"
			if bad || got[k] != want[k] {
				mark, failed = "FAIL", true
			}
			line += fmt.Sprintf("op%d %s %s  ", k+1, want[k], mark)
		}
		fmt.Println(line)
	}
	back, err2 := h.Apply(A, ops)
	ok("roundtrip A->B:", err == nil && err2 == nil && reflect.DeepEqual(back, B))
	e1, d1 := ptr.Encode("~1") == "~01" && ptr.Encode("a/b") == "a~1b", true
	u1, e2 := ptr.Decode("~01")
	u2, e3 := ptr.Decode("a~1b")
	ok("escape ~0/~1:", e1 && d1 && e2 == nil && e3 == nil && u1 == "~1" && u2 == "a/b")
	elem := []api.Op{{Op: "remove", Path: "/arr/1"}, {Op: "remove", Path: "/arr/2"}, {Op: "remove", Path: "/arr/3"}}
	_, errE := h.Apply(A, elem)
	nullOK := len(ops) == 9 && ops[6].Op == "add" && ops[6].HasValue && ops[6].Value == nil && ops[7].Op == "remove"
	ok("elem-remove fail & null-vs-missing:", errors.Is(errE, api.ErrNotFound) && nullOK)
	r := rand.New(rand.NewSource(1))
	rt := true
	for i := 0; i < 200 && rt; i++ {
		a, b := rdoc(r, 3), rdoc(r, 3)
		p, e1 := h.Diff(a, b)
		g, e2 := h.Apply(a, p)
		rt = e1 == nil && e2 == nil && reflect.DeepEqual(g, b)
	}
	ok("random roundtrip x200:", rt)
	snap := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	big := map[string]any{}
	for i := 0; i < 10000; i++ {
		big[fmt.Sprintf("k%05d", i)] = float64(i)
	}
	sA := snap(A)
	_, eP := h.Apply(A, []api.Op{{Op: "replace", Path: "/~2", Value: 1, HasValue: true}})
	_, eN := h.Apply(A, []api.Op{{Op: "replace", Path: "/nope", Value: 1, HasValue: true}})
	_, eO := h.Apply(A, []api.Op{{Op: "move", Path: "/a~1b"}})
	_, eO2 := h.Apply(A, []api.Op{{Op: "add", Path: "/x"}})
	h1 := api.New(1)
	_, eT1 := h1.Diff(A, B)
	_, eT2 := h1.Apply(A, []api.Op{{Op: "remove", Path: "/z"}, {Op: "remove", Path: "/n"}})
	ok("4 error kinds:", errors.Is(eP, api.ErrInvalidPath) && errors.Is(eN, api.ErrNotFound) &&
		errors.Is(eO, api.ErrInvalidOp) && errors.Is(eO2, api.ErrInvalidOp) &&
		errors.Is(eT1, api.ErrTooManyOps) && errors.Is(eT2, api.ErrTooManyOps))
	_, errF := h.Apply(A, append([]api.Op{{Op: "remove", Path: "/z"}}, elem...))
	_, errB := h.Apply(big, []api.Op{{Op: "replace", Path: "/k09999", Value: 0, HasValue: true}})
	ok("fail-keeps-doc & big-m:", errF != nil && snap(A) == sA && errB == nil)
	src := j(`{"a":1,"b":[1,2,3],"c":{"x":1}}`)
	sSrc := snap(src)
	var wg sync.WaitGroup
	ress := make([]any, 64)
	for i := range ress {
		p := []api.Op{{Op: "replace", Path: "/a", Value: float64(i), HasValue: true}}
		if i%2 == 1 {
			p = append(p, api.Op{Op: "remove", Path: "/nope"})
		}
		exp, _ := h.Apply(src, p)
		wg.Add(1)
		go func(i int, p []api.Op, exp any) {
			defer wg.Done()
			g, _ := h.Apply(src, p)
			ress[i] = reflect.DeepEqual(g, exp)
		}(i, p, exp)
	}
	wg.Wait()
	allEq := snap(src) == sSrc
	for _, x := range ress {
		allEq = allEq && x == true
	}
	ok("concurrent apply x64:", allEq)
	if failed {
		os.Exit(1)
	}
}
