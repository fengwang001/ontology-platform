package api

import (
	"encoding/json"
	"errors"
	"math/rand"
	"ontology/patch"
	"ontology/ptr"
	"reflect"
	"sync"
	"testing"
)

func op(o, p string, v any) Op { return Op{Op: o, Path: p, Value: v, HasValue: true} }
func snap(v any) string        { b, _ := json.Marshal(v); return string(b) }
func randDoc(r *rand.Rand, d int) any {
	if d > 2 || r.Intn(2) == 0 {
		return []any{nil, true, 1.5, "s"}[r.Intn(4)]
	}
	if r.Intn(2) == 0 {
		return map[string]any{"a": randDoc(r, d+1), "b/c": randDoc(r, d+1)}
	}
	return []any{randDoc(r, d+1), randDoc(r, d+1)}
}

// checkRT 钉住不变量 1（往返相等）与 2（最小且有序）。
func checkRT(t *testing.T, e *Engine, a, b any) {
	ops, err := e.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Apply(a, ops)
	if err != nil || !reflect.DeepEqual(got, b) {
		t.Fatalf("roundtrip got=%v err=%v", got, err)
	}
	if n := naive(a, b); n != len(ops) {
		t.Fatalf("count %d != naive %d", len(ops), n)
	}
	if err := ordered(ops); err != nil {
		t.Fatal(err)
	}
}
func TestSection3(t *testing.T) {
	a := mustJSON(`{"a/b":1,"arr":[5,6,7,8],"m~n":{"x":1,"y":2},"z":null,"~1":true}`)
	b := mustJSON(`{"a/b":2,"arr":[5],"k":{"q":1},"m~n":{"y":3,"w":[]},"n":null,"~1":false}`)
	want := []Op{
		op("replace", "/a~1b", 2.0), op("replace", "/arr", []any{5.0}), op("add", "/k", map[string]any{"q": 1.0}), op("add", "/m~0n/w", []any{}),
		{Op: "remove", Path: "/m~0n/x"}, op("replace", "/m~0n/y", 3.0), op("add", "/n", nil), {Op: "remove", Path: "/z"}, op("replace", "/~01", false),
	}
	ops, err := New(100).Diff(a, b)
	if err != nil || !reflect.DeepEqual(ops, want) {
		t.Fatalf("ops=%v err=%v", ops, err)
	}
}
func TestRoundTrip(t *testing.T) {
	e := New(1000)
	for _, p := range [][2]string{{`{"a":1}`, `{"a":2}`}, {`[1,2]`, `{"x":1}`}, {`null`, `{"a":[true,null]}`}} {
		checkRT(t, e, mustJSON(p[0]), mustJSON(p[1]))
	}
}
func TestDiffMinimalOrdered(t *testing.T) {
	e := New(1000)
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 200; i++ {
		checkRT(t, e, randDoc(r, 0), randDoc(r, 0))
	}
}

// TestNoMutation 钉住不变量 3：Diff/Apply 不论成败，文档与补丁深度不变。
func TestNoMutation(t *testing.T) {
	e := New(100)
	for i, ops := range [][]Op{{op("add", "/b", []any{1.0})}, {{Op: "remove", Path: "/nope"}}} {
		doc := mustJSON(`{"a":1,"b":[1,2,3]}`)
		d0, o0 := snap(doc), snap(ops)
		_, _ = e.Apply(doc, ops)
		_, _ = e.Diff(doc, mustJSON(`{"z":9}`))
		if snap(doc) != d0 || snap(ops) != o0 {
			t.Errorf("case %d: input mutated", i)
		}
	}
}

// TestAtomicFailure 钉住不变量 4：四类错误可判定、互不相同、失败不留痕、拒绝后仍可用。
func TestAtomicFailure(t *testing.T) {
	cases := [][]Op{
		{{Op: "remove", Path: "x"}}, {{Op: "remove", Path: ""}}, {op("replace", "/arr/01", 0.0)},
		{{Op: "remove", Path: "/nope"}}, {op("add", "/no/such", 1.0)}, {op("replace", "/arr/9", 0.0)},
		{{Op: "move", Path: "/x"}}, {{Op: "add", Path: "/y"}},
	}
	wants := []error{ptr.ErrInvalidPath, ptr.ErrInvalidPath, ptr.ErrInvalidPath,
		ptr.ErrNotFound, ptr.ErrNotFound, ptr.ErrNotFound, patch.ErrInvalidOp, patch.ErrInvalidOp}
	e := New(100)
	for i, c := range cases {
		doc := mustJSON(`{"x":1,"arr":[1,2]}`)
		if _, err := e.Apply(doc, c); !errors.Is(err, wants[i]) {
			t.Errorf("case %d: got %v want %v", i, err, wants[i])
		}
		if snap(doc) != snap(mustJSON(`{"x":1,"arr":[1,2]}`)) {
			t.Errorf("case %d: doc mutated", i)
		}
	}
	if _, err := New(1).Apply(mustJSON(`{"x":1}`), []Op{op("replace", "/x", 1.0), op("replace", "/x", 2.0)}); !errors.Is(err, patch.ErrTooManyOps) {
		t.Error("too many ops not rejected")
	}
	distinct := !errors.Is(ptr.ErrInvalidPath, ptr.ErrNotFound) && !errors.Is(ptr.ErrInvalidPath, patch.ErrInvalidOp) &&
		!errors.Is(ptr.ErrInvalidPath, patch.ErrTooManyOps) && !errors.Is(ptr.ErrNotFound, patch.ErrInvalidOp) &&
		!errors.Is(ptr.ErrNotFound, patch.ErrTooManyOps) && !errors.Is(patch.ErrInvalidOp, patch.ErrTooManyOps)
	if !distinct {
		t.Fatal("sentinels not distinct")
	}
	if got, err := e.Apply(mustJSON(`{"x":1}`), []Op{op("replace", "/x", 2.0)}); err != nil || !reflect.DeepEqual(got, mustJSON(`{"x":2}`)) {
		t.Error("unusable after rejection")
	}
}

// TestConcurrentApply 钉住并发安全：同一源文档并发 Apply，结果与单线程一致，源文档不变。
func TestConcurrentApply(t *testing.T) {
	e := New(100)
	src := mustJSON(`{"a":1,"b":[1,2,3],"c":{"d":1}}`)
	s0 := snap(src)
	var wg sync.WaitGroup
	for i := 0; i < 33; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ops := []Op{op("replace", "/a", float64(i))}
			if i%3 == 0 {
				ops = []Op{{Op: "remove", Path: "/missing"}}
			}
			got, err := e.Apply(src, ops)
			want, wantErr := e.Apply(src, ops)
			if (err == nil) != (wantErr == nil) || !reflect.DeepEqual(got, want) {
				t.Error("apply mismatch")
			}
			if err := e.SelfCheck(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if snap(src) != s0 {
		t.Error("source mutated")
	}
}
func TestSelfCheck(t *testing.T) {
	if err := New(64).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
