package ingest

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/coerce"
	"ontology/mapping"
)

func i64(v int64) any   { return v }
func flt(v float64) any { return v }
func str(v string) any  { return v }
func boo(v bool) any    { return v }
func slc(v ...any) any  { return v }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndexExamples(t *testing.T) {
	st, err := New(mapping.DynamicTrue, 4)
	must(t, err)

	res, err := st.Index("d1", map[string]any{
		"a": map[string]any{"b": i64(1)},
		"c": slc("2", i64(3)),
	})
	must(t, err)
	if res.MV != 1 {
		t.Fatalf("d1 mv=%d", res.MV)
	}
	wantDoc := map[string]any{
		"a": map[string]any{"b": int64(1)},
		"c": []any{"2", "3"},
	}
	if !reflect.DeepEqual(res.Doc, wantDoc) {
		t.Fatalf("d1 doc=%#v", res.Doc)
	}

	_, err = st.Index("d2", map[string]any{"a": map[string]any{"b": "07"}})
	if !errors.Is(err, ErrTypeConflict) || !strings.Contains(err.Error(), "a.b") {
		t.Fatalf("d2 err=%v", err)
	}
	if st.MV() != 1 {
		t.Fatalf("mv after reject = %d", st.MV())
	}

	_, err = st.Index("d3", map[string]any{
		"a": map[string]any{"b": "-7", "x": 1.5},
		"e": true,
	})
	if !errors.Is(err, ErrFieldLimit) || !strings.Contains(err.Error(), "e") {
		t.Fatalf("d3 err=%v", err)
	}
	if st.MV() != 1 {
		t.Fatalf("d3 changed mv: %d", st.MV())
	}
	if _, ok := st.Fields()["a.x"]; ok {
		t.Fatal("d3 left a.x behind")
	}

	_, err = st.Index("d4", map[string]any{"a": i64(5)})
	if !errors.Is(err, ErrTypeConflict) || !strings.Contains(err.Error(), "a") {
		t.Fatalf("d4 err=%v", err)
	}

	res, err = st.Index("d5", map[string]any{
		"a": map[string]any{"b": 3.0, "x": i64(2)},
	})
	must(t, err)
	if res.MV != 2 {
		t.Fatalf("d5 mv=%d", res.MV)
	}
	d5, _ := st.Get("d5")
	if got := d5["a"].(map[string]any)["b"]; got != int64(3) {
		t.Fatalf("d5 a.b=%#v", got)
	}

	_, err = st.Index("d6", map[string]any{"a": map[string]any{"x": 2.5}})
	if !errors.Is(err, ErrTypeConflict) || !strings.Contains(err.Error(), "a.x") {
		t.Fatalf("d6 err=%v", err)
	}

	res, err = st.Index("d7", map[string]any{"c": true, "a": map[string]any{"b": nil}})
	must(t, err)
	if res.MV != 2 {
		t.Fatalf("d7 mv=%d", res.MV)
	}
	d7, _ := st.Get("d7")
	if d7["c"] != "true" {
		t.Fatalf("d7 c=%#v", d7["c"])
	}
	if _, hasB := d7["a"].(map[string]any)["b"]; hasB {
		t.Fatalf("nil should be absent from stored doc: %#v", d7)
	}
}

func TestArrayTypingBothDirections(t *testing.T) {
	st, _ := New(mapping.DynamicTrue, 10)
	res, err := st.Index("d", map[string]any{"k": slc(i64(1), "2")})
	must(t, err)
	if !reflect.DeepEqual(res.Doc["k"], []any{int64(1), int64(2)}) ||
		st.Fields()["k"] != coerce.Long {
		t.Fatalf("int-first: %#v %v", res.Doc["k"], st.Fields()["k"])
	}

	st2, _ := New(mapping.DynamicTrue, 10)
	res, err = st2.Index("d", map[string]any{"k": slc("2", i64(1))})
	must(t, err)
	if !reflect.DeepEqual(res.Doc["k"], []any{"2", "1"}) ||
		st2.Fields()["k"] != coerce.Keyword {
		t.Fatalf("string-first: %#v %v", res.Doc["k"], st2.Fields()["k"])
	}

	st3, _ := New(mapping.DynamicTrue, 10)
	_, err = st3.Index("d", map[string]any{"k": slc(i64(1), 2.5)})
	if !errors.Is(err, ErrTypeConflict) {
		t.Fatalf("mixed reject: %v", err)
	}
	if len(st3.Fields()) != 0 {
		t.Fatalf("rejected array left field: %v", st3.Fields())
	}
}

func TestNilLeadingAndEmptyArrays(t *testing.T) {
	st, _ := New(mapping.DynamicTrue, 10)
	res, err := st.Index("d", map[string]any{"k": slc(nil, i64(1), "2", nil)})
	must(t, err)
	want := []any{nil, int64(1), int64(2), nil}
	if !reflect.DeepEqual(res.Doc["k"], want) {
		t.Fatalf("got %#v", res.Doc["k"])
	}

	res2, err := st.Index("e", map[string]any{"x": slc(nil, nil), "y": slc()})
	must(t, err)
	if _, ok := res2.Doc["x"]; ok {
		t.Fatal("all-nil array should be absent from doc")
	}
	for _, p := range []string{"x", "y"} {
		if _, ok := st.Fields()[p]; ok {
			t.Fatalf("%s should not create field", p)
		}
	}

	res3, err := st.Index("g", map[string]any{"k": nil})
	must(t, err)
	if _, ok := res3.Doc["k"]; ok {
		t.Fatal("nil scalar should be absent from doc but field survives")
	}
	if st.Fields()["k"] != coerce.Long {
		t.Fatal("existing field must remain")
	}
}

func TestNumericBoundaries(t *testing.T) {
	st, _ := New(mapping.DynamicTrue, 20)
	must(t, st.PutMapping([]string{"lo"}, coerce.Long))
	must(t, st.PutMapping([]string{"db"}, coerce.Double))
	cases := []struct {
		path string
		v    any
		ok   bool
	}{
		{"lo", 2.0, true},
		{"lo", 2.5, false},
		{"lo", "07", false},
		{"lo", "-0", false},
		{"db", int64(1) << 53, true},
		{"db", (int64(1) << 53) + 1, false},
		{"db", math.NaN(), false},
		{"db", math.Inf(1), false},
	}
	for _, c := range cases {
		_, err := st.Index("doc", map[string]any{c.path: c.v})
		if c.ok && err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if !c.ok && !errors.Is(err, ErrTypeConflict) {
			t.Fatalf("%+v: err=%v", c, err)
		}
	}
}

func TestObjectLeafCollision(t *testing.T) {
	st, _ := New(mapping.DynamicTrue, 10)
	_, err := st.Index("d", map[string]any{"a": map[string]any{"b": i64(1)}})
	must(t, err)
	_, err = st.Index("e", map[string]any{"a": i64(1)})
	if !errors.Is(err, ErrTypeConflict) || !strings.HasSuffix(err.Error(), "a") {
		t.Fatalf("object->leaf: %v", err)
	}
	must(t, st.PutMapping([]string{"x"}, coerce.Long))
	_, err = st.Index("g", map[string]any{"x": map[string]any{"y": i64(1)}})
	if !errors.Is(err, ErrTypeConflict) || !strings.HasSuffix(err.Error(), "x") {
		t.Fatalf("leaf->object: %v", err)
	}
}

func TestFieldLimitBoundary(t *testing.T) {
	st, _ := New(mapping.DynamicTrue, 3)
	_, err := st.Index("d", map[string]any{
		"a": map[string]any{"b": i64(1)},
		"c": true,
	})
	if err != nil || len(st.Fields()) != 3 {
		t.Fatalf("exact: err=%v fields=%d", err, len(st.Fields()))
	}

	st2, _ := New(mapping.DynamicTrue, 3)
	_, err = st2.Index("d", map[string]any{
		"a": map[string]any{"b": i64(1)},
		"c": slc("z"),
		"e": true,
	})
	if !errors.Is(err, ErrFieldLimit) || !strings.HasSuffix(err.Error(), "e") {
		t.Fatalf("over: %v", err)
	}
	if len(st2.Fields()) != 0 || st2.MV() != 0 {
		t.Fatalf("over left traces: fields=%d mv=%d", len(st2.Fields()), st2.MV())
	}
}

func TestDynamicModes(t *testing.T) {
	st, _ := New(mapping.DynamicFalse, 10)
	res, err := st.Index("d1", map[string]any{
		"a": map[string]any{"b": i64(1), "c": 2.5},
		"c": slc("2", i64(3)),
	})
	must(t, err)
	if len(res.Doc) != 0 || res.MV != 0 {
		t.Fatalf("false: doc=%#v mv=%d", res.Doc, res.MV)
	}
	if strings.Join(res.Ignored, ",") != "a,c" {
		t.Fatalf("ignored=%v", res.Ignored)
	}

	must(t, st.PutMapping([]string{"k"}, coerce.Long))
	res, err = st.Index("d2", map[string]any{"k": "5", "new": true})
	must(t, err)
	if res.Doc["k"] != int64(5) || len(res.Ignored) != 1 || res.Ignored[0] != "new" {
		t.Fatalf("false coerce: %#v %v", res.Doc, res.Ignored)
	}

	st2, _ := New(mapping.DynamicStrict, 10)
	_, err = st2.Index("d", map[string]any{"new": i64(1)})
	if !errors.Is(err, ErrStrict) || !strings.HasSuffix(err.Error(), "new") {
		t.Fatalf("strict: %v", err)
	}
	must(t, st2.PutMapping([]string{"k"}, coerce.Bool))
	res, err = st2.Index("d2", map[string]any{"k": "true"})
	must(t, err)
	if res.Doc["k"] != true {
		t.Fatalf("strict coerce: %#v", res.Doc)
	}
	_, err = st2.Index("d3", map[string]any{"a": map[string]any{"b": i64(1)}})
	if !errors.Is(err, ErrStrict) || !strings.HasSuffix(err.Error(), "a") {
		t.Fatalf("strict nested: %v", err)
	}
}

func TestValidationOverwriteGet(t *testing.T) {
	st, _ := New(mapping.DynamicTrue, 20)
	_, err := st.Index("", map[string]any{"a": i64(1)})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	_, err = st.Index(strings.Repeat("k", 513), map[string]any{"a": i64(1)})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	_, err = st.Index("d", map[string]any{
		"a":       map[string]any{"b": i64(1), "c": i64(2), "d": i64(3), "e": i64(4), "f": i64(5)},
		"bad.key": i64(1),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("validation precedes limit: %v", err)
	}
	if len(st.Fields()) != 0 {
		t.Fatal("structural rejection moved mapping")
	}
	_, err = st.Index("e", map[string]any{"k": slc(i64(1), map[string]any{"x": i64(1)})})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("array object: %v", err)
	}
	_, err = st.Index("t", map[string]any{"k": slc(i64(1), slc(i64(2)))})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nested array: %v", err)
	}

	deep := map[string]any{"k": i64(1)}
	for d := 0; d < 7; d++ {
		deep = map[string]any{"n": deep}
	}
	_, err = st.Index("ok8", deep)
	must(t, err)
	_, err = st.Index("bad9", map[string]any{"n": any(deep)})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("depth 9: %v", err)
	}

	_, err = st.Get("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}

	_, err = st.Index("dup", map[string]any{"k": i64(1)})
	must(t, err)
	_, err = st.Index("dup", map[string]any{"k": "2", "z": true})
	must(t, err)
	got, err := st.Get("dup")
	must(t, err)
	if !reflect.DeepEqual(got, map[string]any{"k": int64(2), "z": true}) {
		t.Fatalf("overwrite: %#v", got)
	}
}

func TestConcurrent(t *testing.T) {
	st, _ := New(mapping.DynamicTrue, 100000)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				id := "w" + string(rune('a'+w)) + "-" + itoa(n)
				doc := map[string]any{
					"g": map[string]any{
						"a": i64(int64(n)),
						"b": slc(nil, "true", boo(n%2 == 0)),
					},
				}
				if _, err := st.Index(id, doc); err != nil {
					t.Errorf("index: %v", err)
					return
				}
				if _, err := st.Get(id); err != nil {
					t.Errorf("get: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if st.MV() != 1 {
		t.Fatalf("all writes converged to one mapping, mv=%d", st.MV())
	}
	for path, typ := range st.Fields() {
		switch path {
		case "g", "g.a", "g.b":
		default:
			t.Fatalf("unexpected field %s:%s", path, typ)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
