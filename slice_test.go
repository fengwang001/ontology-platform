package ontology

import "testing"

func TestNilVsEmptySlice(t *testing.T) {
	c := NewConverter(Strict)

	out, err := c.CoerceValue(nil, Target{Kind: Int64s, Nullable: true})
	if err != nil || !out.IsExplicitNull() {
		t.Fatalf("nil element value: %+v %v", out, err)
	}

	var nilSlice []any
	out, err = c.CoerceValue(nilSlice, Target{Kind: Int64s})
	if err != nil || !out.NilSlice || out.Slice != nil {
		t.Fatalf("nil slice: %+v %v", out, err)
	}

	out, err = c.CoerceValue([]any{}, Target{Kind: Int64s})
	if err != nil || out.NilSlice || out.Slice == nil || len(out.Slice) != 0 {
		t.Fatalf("empty slice must be non-nil zero-length: %+v %v", out, err)
	}
	if !out.IsZeroValue(Int64s) {
		t.Fatalf("empty slice is a present zero value")
	}
}

func TestSliceElementLocation(t *testing.T) {
	c := NewConverter(Strict)
	in := []any{int64(1), "oops", int64(3)}
	_, err := c.CoerceValue(in, Target{Kind: Int64s})
	ce := AsCoerceError(err)
	if ce == nil {
		t.Fatal("expected element failure")
	}
	if ce.Index != 1 {
		t.Fatalf("want index 1, got %d", ce.Index)
	}
	if ce.ElementCategory != CatMalformedNumber {
		t.Fatalf("want element category malformed_number, got %s", ce.ElementCategory)
	}

	// Numeric overflow on element 2.
	in = []any{int64(1), "9223372036854775808"}
	_, err = c.CoerceValue(in, Target{Kind: Int64s})
	ce = AsCoerceError(err)
	if ce.Index != 1 || ce.ElementCategory != CatOverflow {
		t.Fatalf("want index 1 overflow, got idx=%d cat=%s", ce.Index, ce.ElementCategory)
	}

	// Bool slice: element 0 invalid.
	_, err = c.CoerceValue([]any{"yes", true}, Target{Kind: Bools})
	if ce = AsCoerceError(err); ce.Index != 0 || ce.ElementCategory != CatInvalidBool {
		t.Fatalf("want index 0 invalid_bool, got idx=%d cat=%s", ce.Index, ce.ElementCategory)
	}
}

func TestSliceLenientSkipsAndRecords(t *testing.T) {
	c := NewConverter(Lenient)
	in := []any{int64(1), "oops", int64(3), 3.75}
	out, err := c.CoerceValue(in, Target{Kind: Int64s})
	if err != nil {
		t.Fatalf("lenient slice must not fail: %v", err)
	}
	if len(out.Slice) != 2 || out.Slice[0] != int64(1) || out.Slice[1] != int64(3) {
		t.Fatalf("bad elements must be skipped: %+v", out.Slice)
	}
	if len(out.Records) != 2 {
		t.Fatalf("want 2 skip records, got %+v", out.Records)
	}
	if r := out.Records[0]; r.Index != 1 || r.Category != CatMalformedNumber {
		t.Fatalf("record 0 bad: %+v", r)
	}
	if r := out.Records[1]; r.Index != 3 || r.Category != CatFractionLost {
		t.Fatalf("record 1 bad: %+v", r)
	}
	for _, r := range out.Records {
		if r.To != nil {
			t.Fatalf("skipped elements must not insert defaults: %+v", r)
		}
	}
}

func TestSliceStrictLenientCategoryParity(t *testing.T) {
	in := []any{int64(1), "oops", 3.75, 1e20, true}
	tgt := Target{Kind: Int64s}

	_, sErr := NewConverter(Strict).CoerceValue(in, tgt)
	sFirst := AsCoerceError(sErr)
	if sFirst == nil || sFirst.Index != 1 || sFirst.ElementCategory != CatMalformedNumber {
		t.Fatalf("strict: %+v", sErr)
	}

	lOut, err := NewConverter(Lenient).CoerceValue(in, tgt)
	if err != nil || len(lOut.Records) != 4 {
		t.Fatalf("lenient: %+v %v", lOut.Records, err)
	}
	got := []Category{}
	for _, r := range lOut.Records {
		got = append(got, r.Category)
	}
	want := []Category{CatMalformedNumber, CatFractionLost, CatOverflow, CatTypeMismatch}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lenient records %v, want %v", got, want)
		}
	}
}

func TestTypedSlicesSupported(t *testing.T) {
	c := NewConverter(Strict)
	out, err := c.CoerceValue([]string{"1", "2"}, Target{Kind: Int64s})
	if err != nil {
		t.Fatalf("typed slice: %v", err)
	}
	if len(out.Slice) != 2 || out.Slice[0] != int64(1) || out.Slice[1] != int64(2) {
		t.Fatalf("typed slice result: %+v", out.Slice)
	}
}
