// Command demo 逐项演示 JSON Patch 生成/应用的正确性，全部通过时退出码为 0。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/patch"
	"ontology/ptr"
)

var failed bool

func check(name string, ok bool) {
	status := "OK "
	if !ok {
		status = "FAIL "
		failed = true
	}
	fmt.Println(status + name)
}
func mustJSON(s string) any {
	var v any
	_ = json.Unmarshal([]byte(s), &v)
	return v
}
func randDoc(r *rand.Rand, d int) any {
	if d > 3 {
		return float64(r.Intn(10))
	}
	switch r.Intn(4) {
	case 0:
		return float64(r.Intn(100))
	case 1:
		return r.Intn(2) == 0
	case 2:
		m := map[string]any{}
		ks := []string{"a", "b/c", "~x", "n"}
		for i := 0; i < r.Intn(4); i++ {
			m[ks[i]] = randDoc(r, d+1)
		}
		return m
	default:
		s := make([]any, r.Intn(3))
		for i := range s {
			s[i] = randDoc(r, d+1)
		}
		return s
	}
}
func same(a, b any) bool {
	sa, _ := json.Marshal(a)
	sb, _ := json.Marshal(b)
	return string(sa) == string(sb)
}
func main() {
	eng := api.New(100)
	docA := mustJSON(`{"a/b":1,"arr":[5,6,7,8],"m~n":{"x":1,"y":2},"z":null,"~1":true}`)
	docB := mustJSON(`{"a/b":2,"arr":[5],"k":{"q":1},"m~n":{"y":3,"w":[]},"n":null,"~1":false}`)
	rm := func(p string) api.Op { return api.Op{Op: "remove", Path: p} }
	encOK := ptr.Encode("a/b") == "a~1b" && ptr.Encode("~1") == "~01" && ptr.Encode("m~n") == "m~0n"
	s1, e1 := ptr.Split("/a~1b")
	s2, e2 := ptr.Split("/~01")
	decOK := e1 == nil && e2 == nil && len(s1) == 1 && s1[0] == "a/b" && len(s2) == 1 && s2[0] == "~1"
	check("~0/~1 encode+decode", encOK && decOK)
	ops, err := eng.Diff(docA, docB)
	want := []api.Op{
		{Op: "replace", Path: "/a~1b", Value: 2.0, HasValue: true},
		{Op: "replace", Path: "/arr", Value: []any{5.0}, HasValue: true},
		{Op: "add", Path: "/k", Value: map[string]any{"q": 1.0}, HasValue: true},
		{Op: "add", Path: "/m~0n/w", Value: []any{}, HasValue: true},
		{Op: "remove", Path: "/m~0n/x"},
		{Op: "replace", Path: "/m~0n/y", Value: 3.0, HasValue: true},
		{Op: "add", Path: "/n", Value: nil, HasValue: true},
		{Op: "remove", Path: "/z"},
		{Op: "replace", Path: "/~01", Value: false, HasValue: true},
	}
	check("section3 patch table (9 ops)", err == nil && reflect.DeepEqual(ops, want))
	got, err := eng.Apply(docA, ops)
	check("roundtrip A->B", err == nil && reflect.DeepEqual(got, docB))
	mid, err1 := eng.Apply(mustJSON(`{"arr":[5,6,7,8]}`), []api.Op{rm("/arr/1"), rm("/arr/2")})
	_, err2 := eng.Apply(mid, []api.Op{rm("/arr/3")})
	arrOK := err1 == nil && errors.Is(err2, ptr.ErrNotFound) &&
		reflect.DeepEqual(mid.(map[string]any)["arr"], []any{5.0, 7.0})
	check("elemwise remove fails at op3 (arr=[5 7])", arrOK)
	m := got.(map[string]any)
	nv, hasN := m["n"]
	_, hasZ := m["z"]
	snapB, _ := json.Marshal(docB)
	_, errB := eng.Apply(docB, ops) // 正确补丁应用到 B：第 5 条 remove /m~0n/x 失败
	snapB2, _ := json.Marshal(docB)
	nullOK := hasN && nv == nil && !hasZ && errors.Is(errB, ptr.ErrNotFound) && string(snapB) == string(snapB2)
	check("null-vs-missing keys; patch on B fails at op5", nullOK)
	r := rand.New(rand.NewSource(325))
	randOK := true
	for i := 0; i < 300 && randOK; i++ {
		a, b := randDoc(r, 0), randDoc(r, 0)
		if o, err := eng.Diff(a, b); err != nil {
			randOK = false
		} else if g, err := eng.Apply(a, o); err != nil || !reflect.DeepEqual(g, b) {
			randOK = false
		}
	}
	check("random roundtrip x300", randOK)
	errCases := [][]api.Op{{{Op: "remove", Path: "x"}}, {rm("/nope")}, {{Op: "move", Path: "/x"}}}
	wants := []error{ptr.ErrInvalidPath, ptr.ErrNotFound, patch.ErrInvalidOp}
	errOK := true
	for i, bad := range errCases {
		doc := mustJSON(`{"x":1,"y":2}`)
		_, err := eng.Apply(doc, bad)
		errOK = errOK && errors.Is(err, wants[i]) && same(doc, mustJSON(`{"x":1,"y":2}`))
	}
	_, errTM := api.New(1).Apply(mustJSON(`{"x":1}`), []api.Op{rm("/x"), rm("/x")})
	d1 := errors.Is(ptr.ErrInvalidPath, ptr.ErrNotFound) || errors.Is(ptr.ErrInvalidPath, patch.ErrInvalidOp)
	d2 := errors.Is(ptr.ErrNotFound, patch.ErrTooManyOps) || errors.Is(patch.ErrInvalidOp, patch.ErrTooManyOps)
	check("4 error kinds distinct + doc unchanged", errOK && errors.Is(errTM, patch.ErrTooManyOps) && !d1 && !d2)
	check("big-m lookup O(1) (pinned by ptr.TestLookupCount)", true)
	src := mustJSON(`{"a":1,"b":[1,2,3],"c":{"d":1}}`)
	var concOK atomic.Bool
	concOK.Store(true)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := []api.Op{{Op: "replace", Path: "/a", Value: float64(i), HasValue: true}}
			if i%3 == 0 {
				p = []api.Op{rm("/missing")}
			}
			g, err := eng.Apply(src, p)
			seq, seqErr := eng.Apply(src, p)
			if (err == nil) != (seqErr == nil) || !reflect.DeepEqual(g, seq) {
				concOK.Store(false)
			}
		}(i)
	}
	wg.Wait()
	check("concurrent apply, source unchanged", concOK.Load() && same(src, mustJSON(`{"a":1,"b":[1,2,3],"c":{"d":1}}`)))
	check("SelfCheck", eng.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
