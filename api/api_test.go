package api_test

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ptr"
)

func j(s string) any    { var v any; _ = json.Unmarshal([]byte(s), &v); return v }
func snap(v any) string { b, _ := json.Marshal(v); return string(b) }

func rdoc(r *rand.Rand, d int) any {
	k := r.Intn(4)
	if d > 0 && k == 2 {
		m := map[string]any{}
		for i := r.Intn(4); i >= 0; i-- {
			m[string(rune('a'+r.Intn(3)))] = rdoc(r, d-1)
		}
		return m
	}
	if d > 0 && k == 3 {
		s := []any{}
		for i := r.Intn(3); i >= 0; i-- {
			s = append(s, rdoc(r, d-1))
		}
		return s
	}
	return []any{float64(r.Intn(50)), r.Intn(2) == 0, nil, "s"}[r.Intn(4)]
}

func naive(a, b any) int {
	if reflect.DeepEqual(a, b) {
		return 0
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if !aok || !bok {
		return 1
	}
	n := 0
	for k, av := range am {
		if bv, ok := bm[k]; ok {
			n += naive(av, bv)
		} else {
			n++
		}
	}
	for k := range bm {
		if _, ok := am[k]; !ok {
			n++
		}
	}
	return n
}

func leq(a, b []string) bool { // a <= b：逐段字节序，前缀也算
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return true
}

func TestRoundTrip(t *testing.T) { // 不变量 1：往返相等
	h := api.New(1000)
	pairs := [][2]string{
		{`{"a/b":1,"arr":[5,6,7,8],"m~n":{"x":1,"y":2},"z":null,"~1":true}`,
			`{"a/b":2,"arr":[5],"k":{"q":1},"m~n":{"y":3,"w":[]},"n":null,"~1":false}`},
		{`{}`, `{"k":[null,{"~":"~"}]}`}, {`[1,2]`, `{"0":0}`}, {`1`, `null`}, {`{"a":1}`, `{}`},
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 300; i++ {
		a, b := rdoc(r, 4), rdoc(r, 4)
		if i < len(pairs) {
			a, b = j(pairs[i][0]), j(pairs[i][1])
		}
		ops, err := h.Diff(a, b)
		if err != nil {
			t.Fatal(err)
		}
		got, err := h.Apply(a, ops)
		if err != nil || !reflect.DeepEqual(got, b) {
			t.Fatalf("case %d: got=%v err=%v", i, got, err)
		}
	}
}

func TestMinimalOrdered(t *testing.T) { // 不变量 2：最小且有序
	h := api.New(1000)
	r := rand.New(rand.NewSource(9))
	for i := 0; i < 200; i++ {
		a, b := rdoc(r, 4), rdoc(r, 4)
		ops, err := h.Diff(a, b)
		if err != nil || len(ops) != naive(a, b) {
			t.Fatalf("case %d: |ops|=%d naive=%d", i, len(ops), naive(a, b))
		}
		if n, _ := h.Diff(a, a); len(n) != 0 {
			t.Fatalf("case %d: Diff(A,A) not empty", i)
		}
		var prev []string
		for k, o := range ops {
			cur, _ := ptr.Split(o.Path)
			if k > 0 && leq(cur, prev) {
				t.Fatalf("case %d: order %v then %v", i, prev, cur)
			}
			prev = cur
		}
	}
}

func TestNoMutation(t *testing.T) { // 不变量 3：输入不被修改
	h := api.New(100)
	a, b := j(`{"x":[1,2,{"y":null}],"k":1}`), j(`{"x":[1],"z":2}`)
	sa, sb := snap(a), snap(b)
	ops, _ := h.Diff(a, b)
	if _, err := h.Apply(a, ops); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Apply(a, []api.Op{{Op: "remove", Path: "/x/9"}}); err == nil {
		t.Fatal("expected failure")
	}
	if snap(a) != sa || snap(b) != sb {
		t.Fatal("input mutated")
	}
}

func TestFailureAtomic(t *testing.T) { // 不变量 4：失败不留痕
	h := api.New(2)
	doc := j(`{"a":[1,2],"b":{"c":1}}`)
	s := snap(doc)
	bad := [][]api.Op{
		{{Op: "remove", Path: "/b/c"}, {Op: "remove", Path: "/b/zz"}},
		{{Op: "replace", Path: "/a/0", Value: 9.0, HasValue: true}, {Op: "add", Path: "/a/9", Value: 1.0, HasValue: true}},
		{{Op: "remove", Path: "/b/c"}, {Op: "remove", Path: ""}},
		{{Op: "remove", Path: "/b/c"}, {Op: "add", Path: "/x", Value: 1.0}, {Op: "remove", Path: "/a/0"}},
	}
	for i, ops := range bad {
		if _, err := h.Apply(doc, ops); err == nil || snap(doc) != s {
			t.Fatalf("case %d: err=%v doc=%s", i, err, snap(doc))
		}
	}
	if _, err := h.Apply(doc, []api.Op{{Op: "remove", Path: "/b/c"}}); err != nil {
		t.Fatal("handler unusable after rejection")
	}
}

func TestErrorKinds(t *testing.T) { // 四类可判定错误互不相同
	h := api.New(1)
	doc := j(`{"a":[1],"b":1}`)
	cases := []struct {
		ops  []api.Op
		want error
	}{
		{[]api.Op{{Op: "replace", Path: "a", Value: 1.0, HasValue: true}}, api.ErrInvalidPath},
		{[]api.Op{{Op: "replace", Path: "/~3", Value: 1.0, HasValue: true}}, api.ErrInvalidPath},
		{[]api.Op{{Op: "replace", Path: "/a/01", Value: 1.0, HasValue: true}}, api.ErrInvalidPath},
		{[]api.Op{{Op: "remove", Path: ""}}, api.ErrInvalidPath},
		{[]api.Op{{Op: "replace", Path: "/zz", Value: 1.0, HasValue: true}}, api.ErrNotFound},
		{[]api.Op{{Op: "remove", Path: "/a/5"}}, api.ErrNotFound},
		{[]api.Op{{Op: "move", Path: "/b"}}, api.ErrInvalidOp},
		{[]api.Op{{Op: "add", Path: "/b"}}, api.ErrInvalidOp},
		{[]api.Op{{Op: "remove", Path: "/b"}, {Op: "remove", Path: "/a/0"}}, api.ErrTooManyOps},
	}
	for i, c := range cases {
		if _, err := h.Apply(doc, c.ops); !errors.Is(err, c.want) {
			t.Errorf("case %d: err=%v want %v", i, err, c.want)
		}
	}
	kinds := []error{api.ErrInvalidPath, api.ErrNotFound, api.ErrInvalidOp, api.ErrTooManyOps}
	for i, x := range kinds {
		for k, y := range kinds {
			if i != k && errors.Is(x, y) {
				t.Errorf("kinds %d/%d not distinct", i, k)
			}
		}
	}
	if err := api.New(100).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentApply(t *testing.T) { // 并发 Apply：源文档不变、结果等同单线程
	h := api.New(10)
	src := j(`{"a":1,"b":[1,2,3],"c":{"x":1}}`)
	s := snap(src)
	var wg sync.WaitGroup
	ok := make([]bool, 64)
	for i := range ok {
		p := []api.Op{{Op: "replace", Path: "/a", Value: float64(i), HasValue: true}}
		if i%2 == 1 {
			p = append(p, api.Op{Op: "remove", Path: "/nope"})
		}
		exp, _ := h.Apply(src, p)
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _ := h.Apply(src, p)
			ok[i] = reflect.DeepEqual(got, exp)
		}()
	}
	wg.Wait()
	for i, v := range ok {
		if !v {
			t.Errorf("goroutine %d differs from sequential", i)
		}
	}
	if snap(src) != s {
		t.Error("source mutated under concurrency")
	}
}
