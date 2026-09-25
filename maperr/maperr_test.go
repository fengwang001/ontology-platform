package maperr

import (
	"errors"
	"testing"

	"ontology/aggregate"
	"ontology/errdef"
	"ontology/wrap"
)

func TestHTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", errdef.NotFound("x"), 404},
		{"already exists", errdef.AlreadyExists("x"), 409},
		{"conflict", errdef.Conflict("x"), 409},
		{"validation", errdef.Validation("x"), 400},
		{"permission", errdef.PermissionDenied("x"), 403},
		{"internal", errdef.Internal("x"), 500},
		{"unknown degrades 500", errdef.New(errdef.Code("weird"), "x"), 500},
		{"nil is 200", nil, 200},
		{"aggregate uses first child", aggregate.New(
			errdef.NotFound("a"), errdef.Conflict("b")), 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTTPStatus(tc.err); got != tc.want {
				t.Fatalf("HTTPStatus = %d, want %d", got, tc.want)
			}
		})
	}
}

func chainLen(err error) int {
	n := 0
	for err != nil {
		n++
		err = errors.Unwrap(err)
	}
	return n
}

func TestRoundTripFidelity(t *testing.T) {
	targets := []error{
		errdef.ErrNotFound, errdef.ErrAlreadyExists, errdef.ErrConflict,
		errdef.ErrValidation, errdef.ErrPermissionDenied, errdef.ErrInternal,
		errdef.ErrBase,
	}
	codes := []errdef.Code{
		errdef.CodeNotFound, errdef.CodeAlreadyExists, errdef.CodeConflict,
		errdef.CodeValidation, errdef.CodePermissionDenied, errdef.CodeInternal,
		errdef.Code("future_unknown"),
	}
	depths := []int{0, 1, 3}
	for _, code := range codes {
		for _, depth := range depths {
			t.Run(string(code)+"/wrap"+string(rune('0'+depth))+"-single", func(t *testing.T) {
				var e error = errdef.New(code, "msg",
					errdef.WithObject("obj"), errdef.WithProperty("prop"))
				for range depth {
					e = wrap.Wrap(e, wrap.Ctx{Object: "wobj", Property: "wprop"})
				}
				data, err := Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				got, err := Unmarshal(data)
				if err != nil {
					t.Fatal(err)
				}
				for _, target := range targets {
					if errors.Is(e, target) != errors.Is(got, target) {
						t.Fatalf("Is(%v) changed after round-trip", target)
					}
				}
				leaf := got
				for errors.Unwrap(leaf) != nil {
					leaf = errors.Unwrap(leaf)
				}
				lp := leaf.(errdef.ContextProvider)
				if lp.Object() != "obj" || lp.Property() != "prop" {
					t.Fatalf("leaf context lost: %q/%q", lp.Object(), lp.Property())
				}
				op := got.(errdef.ContextProvider)
				wantObj, wantProp := "obj", "prop"
				if depth > 0 {
					wantObj, wantProp = "wobj", "wprop"
				}
				if op.Object() != wantObj || op.Property() != wantProp {
					t.Fatalf("outer context: %q/%q, want %q/%q",
						op.Object(), op.Property(), wantObj, wantProp)
				}
				if chainLen(got) != chainLen(e) {
					t.Fatalf("cause chain length: got %d, want %d",
						chainLen(got), chainLen(e))
				}
			})
		}
	}
}

func TestRoundTripAggregate(t *testing.T) {
	forms := []error{
		aggregate.New(
			errdef.NotFound("a", errdef.WithObject("o1")),
			wrap.Wrap(errdef.Conflict("b"), wrap.Ctx{Object: "o2"}),
			errdef.New(errdef.Code("future"), "c"),
		),
		aggregate.New(
			errdef.Validation("outer"),
			aggregate.New(errdef.NotFound("nested1"), errdef.Internal("nested2")),
		),
	}
	for i, e := range forms {
		data, err := Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Unmarshal(data)
		if err != nil {
			t.Fatalf("form %d: %v", i, err)
		}
		for _, target := range []error{
			errdef.ErrNotFound, errdef.ErrConflict, errdef.ErrValidation, errdef.ErrBase,
		} {
			if errors.Is(e, target) != errors.Is(got, target) {
				t.Fatalf("form %d: Is(%v) changed", i, target)
			}
		}
		before, after := e.(aggregate.Aggregate), got.(aggregate.Aggregate)
		if after.Len() != before.Len() {
			t.Fatalf("form %d: child count %d != %d", i, after.Len(), before.Len())
		}
		for j := range before.All() {
			if before.At(j).Error() != after.At(j).Error() {
				t.Fatalf("form %d child %d message drift", i, j)
			}
		}
	}
}

func TestUnknownCodeDoesNotPanic(t *testing.T) {
	raw := []byte(`{"kind":"typed","code":"brand_new_code","message":"x"}`)
	got, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.(errdef.CodeProvider).ErrCode() != errdef.Code("brand_new_code") {
		t.Fatal("unknown code must be retained, not erased")
	}
	if HTTPStatus(got) != 500 {
		t.Fatal("unknown code must map to 500")
	}
	same := errdef.New(errdef.Code("brand_new_code"), "y")
	if !errors.Is(got, same) {
		t.Fatal("unknown-code errors with same code must Is-match")
	}
}
