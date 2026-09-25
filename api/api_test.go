package api

import (
	"maps"
	"sync"
	"testing"
)

func specValidator(t *testing.T) *Validator {
	t.Helper()
	v, err := New(specFields())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

func TestEightEventSequence(t *testing.T) {
	v := specValidator(t)
	steps := []struct {
		ev   map[string]Value
		want map[string]Value // nil means rejection
		err  error
	}{
		{map[string]Value{"id": IntVal(1), "name": StrVal("a")},
			map[string]Value{"id": IntVal(1), "name": StrVal("a"), "score": IntVal(0), "active": BoolVal(false)}, nil},
		{map[string]Value{"id": IntVal(2), "name": StrVal("b"), "score": IntVal(5)},
			map[string]Value{"id": IntVal(2), "name": StrVal("b"), "score": IntVal(5), "active": BoolVal(false)}, nil},
		{map[string]Value{"id": IntVal(3)}, nil, ErrMissingRequired},
		{map[string]Value{"id": IntVal(4), "name": StrVal("c"), "active": BoolVal(true)},
			map[string]Value{"id": IntVal(4), "name": StrVal("c"), "score": IntVal(0), "active": BoolVal(true)}, nil},
		{map[string]Value{"id": IntVal(5), "name": StrVal("d"), "extra": IntVal(123)},
			map[string]Value{"id": IntVal(5), "name": StrVal("d"), "score": IntVal(0), "active": BoolVal(false)}, nil},
		{map[string]Value{"id": IntVal(6), "name": IntVal(7)}, nil, ErrTypeMismatch},
		{map[string]Value{"id": IntVal(7), "name": StrVal("f"), "score": IntVal(9), "active": BoolVal(false)},
			map[string]Value{"id": IntVal(7), "name": StrVal("f"), "score": IntVal(9), "active": BoolVal(false)}, nil},
		{map[string]Value{"id": IntVal(8), "name": StrVal("g"), "active": StrVal("yes")}, nil, ErrTypeMismatch},
	}
	for i, st := range steps {
		got, err := v.Validate(st.ev)
		bad := err != st.err || (st.err != nil && got != nil) || (st.err == nil && !maps.Equal(got, st.want))
		if bad {
			t.Errorf("step %d: got (%v, %v), want record %v err %v", i+1, got, err, st.want, st.err)
		}
	}
}

func TestValidateMatchesNaive(t *testing.T) {
	v := specValidator(t)
	for i := 0; i < 512; i++ {
		ev := genEvent(i)
		got, gerr := v.Validate(ev)
		want, werr := naive(specFields(), ev)
		if gerr != werr || (gerr == nil && !maps.Equal(got, want)) {
			t.Fatalf("event %d %v: got (%v,%v), want (%v,%v)", i, ev, got, gerr, want, werr)
		}
	}
}

func TestRejectDistinct(t *testing.T) {
	v := specValidator(t)
	_, miss := v.Validate(map[string]Value{"id": IntVal(1)})
	_, typ := v.Validate(map[string]Value{"id": IntVal(1), "name": IntVal(2)})
	d := IntVal(0)
	_, sch := New([]Field{{Name: "a", Type: Int, Required: true, Default: &d}})
	if miss != ErrMissingRequired || typ != ErrTypeMismatch || sch != ErrRequiredHasDefault {
		t.Fatalf("wrong sentinels: %v %v %v", miss, typ, sch)
	}
	if miss == typ || miss == sch || typ == sch {
		t.Fatal("the three rejection categories must be mutually distinct")
	}
}

func TestInvalidSchemaRejected(t *testing.T) {
	good := specValidator(t)
	before, _ := good.Validate(map[string]Value{"id": IntVal(1), "name": StrVal("a")})
	d := IntVal(0)
	cases := []struct {
		name string
		defs []Field
		want error
	}{
		{"dup", []Field{{Name: "a", Type: Int, Required: true}, {Name: "a", Type: Int, Required: true}}, ErrDuplicateField},
		{"empty", []Field{{Name: "", Type: Int, Required: true}}, ErrEmptyFieldName},
		{"req+def", []Field{{Name: "a", Type: Int, Required: true, Default: &d}}, ErrRequiredHasDefault},
		{"opt-nodef", []Field{{Name: "a", Type: Int}}, ErrOptionalNoDefault},
	}
	for _, c := range cases {
		v, err := New(c.defs)
		if err != c.want || v != nil {
			t.Fatalf("%s: New: (%v,%v), want nil + %v", c.name, v, err, c.want)
		}
		if err == ErrMissingRequired || err == ErrTypeMismatch {
			t.Fatalf("%s: illegal-schema error must differ from validate errors", c.name)
		}
	}
	after, _ := good.Validate(map[string]Value{"id": IntVal(1), "name": StrVal("a")})
	if !maps.Equal(before, after) {
		t.Fatal("rejected New changed existing validator state")
	}
}

func TestRejectLeavesNoTrace(t *testing.T) {
	v := specValidator(t)
	ev := map[string]Value{"id": IntVal(1), "name": StrVal("a")}
	before, err := v.Validate(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]Value{{"id": IntVal(2)}, {"id": IntVal(3), "name": IntVal(4)}} {
		if out, err := v.Validate(bad); err == nil || out != nil {
			t.Fatal("rejection must yield nil record and a decidable error")
		}
	}
	after, err := v.Validate(ev)
	if err != nil || !maps.Equal(before, after) || v.SelfCheck() != nil {
		t.Fatal("state changed after rejections")
	}
}

func TestConcurrentValidateIdentical(t *testing.T) {
	v := specValidator(t)
	ev := map[string]Value{"id": IntVal(7), "name": StrVal("f"), "extra": IntVal(1)}
	want, err := v.Validate(ev)
	if err != nil {
		t.Fatal(err)
	}
	res := make([]map[string]Value, 64)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := v.Validate(ev)
			if err != nil {
				t.Error(err)
			}
			res[g] = r
			if err := v.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for g, r := range res {
		if !maps.Equal(r, want) {
			t.Fatalf("goroutine %d: record differs: %v vs %v", g, r, want)
		}
	}
}
