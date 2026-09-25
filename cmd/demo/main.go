package main

import (
	"fmt"
	"maps"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(ok bool, msg string) {
	if ok {
		fmt.Println("OK " + msg)
	} else {
		fmt.Println("FAIL " + msg)
		failed = true
	}
}

func spec() *api.Validator {
	zero, off := api.IntVal(0), api.BoolVal(false)
	v, err := api.New([]api.Field{
		{Name: "id", Type: api.Int, Required: true},
		{Name: "name", Type: api.Str, Required: true},
		{Name: "score", Type: api.Int, Default: &zero},
		{Name: "active", Type: api.Bool, Default: &off},
	})
	if err != nil {
		fmt.Println("FAIL spec schema: " + err.Error())
		os.Exit(1)
	}
	return v
}

func main() {
	v := spec()
	steps := []struct {
		ev   map[string]api.Value
		want error
	}{
		{map[string]api.Value{"id": api.IntVal(1), "name": api.StrVal("a")}, nil},
		{map[string]api.Value{"id": api.IntVal(2), "name": api.StrVal("b"), "score": api.IntVal(5)}, nil},
		{map[string]api.Value{"id": api.IntVal(3)}, api.ErrMissingRequired},
		{map[string]api.Value{"id": api.IntVal(4), "name": api.StrVal("c"), "active": api.BoolVal(true)}, nil},
		{map[string]api.Value{"id": api.IntVal(5), "name": api.StrVal("d"), "extra": api.IntVal(123)}, nil},
		{map[string]api.Value{"id": api.IntVal(6), "name": api.IntVal(7)}, api.ErrTypeMismatch},
		{map[string]api.Value{"id": api.IntVal(7), "name": api.StrVal("f"), "score": api.IntVal(9), "active": api.BoolVal(false)}, nil},
		{map[string]api.Value{"id": api.IntVal(8), "name": api.StrVal("g"), "active": api.StrVal("yes")}, api.ErrTypeMismatch},
	}
	ok := true
	for i, st := range steps {
		_, err := v.Validate(st.ev)
		ok = ok && err == st.want
		_ = i
	}
	check(ok, "eight-step verdicts (3=missing-required, 6/8=type-mismatch)")
	r5, _ := v.Validate(steps[4].ev)
	want5 := map[string]api.Value{"id": api.IntVal(5), "name": api.StrVal("d"), "score": api.IntVal(0), "active": api.BoolVal(false)}
	check(maps.Equal(r5, want5), "step5: extra dropped, defaults filled")
	r1, _ := v.Validate(steps[0].ev)
	check(r1["score"] == api.IntVal(0) && r1["active"] == api.BoolVal(false), "optional defaults filled")
	bads := []api.Field{{Name: "id", Type: api.Int, Required: true, Default: ptr(api.IntVal(0))},
		{Name: "", Type: api.Int, Required: true}, {Name: "x", Type: api.Int}}
	ok = true
	for _, b := range bads {
		_, err := api.New([]api.Field{b})
		ok = ok && err != nil && err != api.ErrMissingRequired && err != api.ErrTypeMismatch
	}
	_, derr := api.New([]api.Field{{Name: "a", Type: api.Int, Required: true}, {Name: "a", Type: api.Int, Required: true}})
	check(ok && derr == api.ErrDuplicateField, "illegal schema rejected (4 kinds, distinct)")
	before, _ := v.Validate(steps[0].ev)
	v.Validate(steps[2].ev)
	v.Validate(steps[5].ev)
	after, _ := v.Validate(steps[0].ev)
	check(maps.Equal(before, after), "no trace after rejections")
	big := make([]api.Field, 10000)
	for i := range big {
		d := api.IntVal(0)
		big[i] = api.Field{Name: fmt.Sprintf("f%d", i), Type: api.Int, Default: &d}
	}
	bv, err := api.New(big)
	_, verr := bv.Validate(map[string]api.Value{"f7": api.IntVal(1)})
	check(err == nil && verr == nil, "large-m validate ok (check-count pinned in tests)")
	ev := map[string]api.Value{"id": api.IntVal(9), "name": api.StrVal("z")}
	want, _ := v.Validate(ev)
	var wg sync.WaitGroup
	res := make([]map[string]api.Value, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, _ := v.Validate(ev); res[g] = r }()
	}
	wg.Wait()
	ok = true
	for _, r := range res {
		ok = ok && maps.Equal(r, want)
	}
	check(ok, "concurrent validate identical")
	check(v.SelfCheck() == nil, "SelfCheck")
	if failed {
		os.Exit(1)
	}
}

func ptr(v api.Value) *api.Value { return &v }
