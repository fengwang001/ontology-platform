package otdoc

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func op(comps ...Component) Operation { return Operation(comps) }

func mustNew(t *testing.T, maxLen int) *Document {
	t.Helper()
	d, err := New(maxLen)
	if err != nil {
		t.Fatalf("New(%d): %v", maxLen, err)
	}
	return d
}

func mustSubmit(t *testing.T, d *Document, site string, seq, baseRev int, o Operation) Result {
	t.Helper()
	res, err := d.Submit(site, seq, baseRev, o)
	if err != nil {
		t.Fatalf("Submit(%q,%d,%d,%v): %v", site, seq, baseRev, o, err)
	}
	return res
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func wantResult(t *testing.T, got Result, rev int, applied bool, o Operation) {
	t.Helper()
	want := Result{Rev: rev, Applied: applied, Op: o}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got result %+v, want %+v", got, want)
	}
}

func TestNewValidation(t *testing.T) {
	for _, n := range []int{-1, 0, 1_000_001} {
		if _, err := New(n); !errors.Is(err, ErrInvalid) {
			t.Fatalf("New(%d): got %v, want ErrInvalid", n, err)
		}
	}
	mustNew(t, 1)
	mustNew(t, 1_000_000)
}

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want Operation }{
		{op(Retain(1), Retain(2), Delete(1), Delete(2)), op(Retain(3), Delete(3))},
		{op(Delete(1), Insert("x")), op(Insert("x"), Delete(1))},
		{op(Insert("a"), Delete(1), Insert("b")), op(Insert("ab"), Delete(1))},
		{op(Delete(1), Insert("x"), Delete(2)), op(Insert("x"), Delete(3))},
		{op(Insert("a"), Insert("b"), Retain(1)), op(Insert("ab"), Retain(1))},
		{op(Retain(2)), op(Retain(2))},
	}
	for i, tc := range cases {
		if got := Normalize(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("case %d: Normalize(%v) = %v, want %v", i, tc.in, got, tc.want)
		}
	}
}

// Spec example 1: doc abcde, stored [R2,D2,R1], new [R3,I"X",R2]
// transforms to [R2,I"X",R1], doc abXe.
func TestExampleRetainOverDelete(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("abcde")))
	mustSubmit(t, d, "b", 1, 1, op(Retain(2), Delete(2), Retain(1)))
	res := mustSubmit(t, d, "c", 1, 1, op(Retain(3), Insert("X"), Retain(2)))
	wantResult(t, res, 3, true, op(Retain(2), Insert("X"), Retain(1)))
	if got := d.Doc(); got != "abXe" {
		t.Fatalf("doc = %q, want %q", got, "abXe")
	}
}

// Spec example 2: doc ab, stored [R1,I"P",R1], new [R1,I"Q",R1]
// transforms to [R2,I"Q",R1], doc aPQb (stored insert stays left).
func TestExampleInsertInsertOrder(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("ab")))
	mustSubmit(t, d, "b", 1, 1, op(Retain(1), Insert("P"), Retain(1)))
	res := mustSubmit(t, d, "c", 1, 1, op(Retain(1), Insert("Q"), Retain(1)))
	wantResult(t, res, 3, true, op(Retain(2), Insert("Q"), Retain(1)))
	if got := d.Doc(); got != "aPQb" {
		t.Fatalf("doc = %q, want %q", got, "aPQb")
	}
}

// Spec example 3: identical overlapping deletes [R1,D2,R1] are removed only
// once; the transform is the no-op [R2].
func TestExampleOverlappingDeletes(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("abcd")))
	mustSubmit(t, d, "b", 1, 1, op(Retain(1), Delete(2), Retain(1)))
	res := mustSubmit(t, d, "c", 1, 1, op(Retain(1), Delete(2), Retain(1)))
	wantResult(t, res, 2, false, op(Retain(2)))
	if got := d.Doc(); got != "ad" {
		t.Fatalf("doc = %q, want %q", got, "ad")
	}
	if got := d.Rev(); got != 2 {
		t.Fatalf("rev = %d, want 2 (no-op must not create a revision)", got)
	}
}

// Two inserts falling into the same deleted region are ordered by storage
// order: the operation stored first ends up left of the later one.
func TestInsertsInSameDeletedRegionKeepStorageOrder(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("abcd")))
	// s1 deletes "bc" (region [1,3)).
	mustSubmit(t, d, "s1", 1, 1, op(Retain(1), Delete(2), Retain(1)))
	// c1 inserts X after 'b' (gap 2, inside the deleted region).
	res1 := mustSubmit(t, d, "c1", 1, 1, op(Retain(2), Insert("X"), Retain(2)))
	wantResult(t, res1, 3, true, op(Retain(1), Insert("X"), Retain(1)))
	// c2 inserts Y after 'a' (gap 1, also inside the deleted region).
	res2 := mustSubmit(t, d, "c2", 1, 1, op(Retain(1), Insert("Y"), Retain(3)))
	wantResult(t, res2, 4, true, op(Retain(2), Insert("Y"), Retain(1)))
	if got := d.Doc(); got != "aXYd" {
		t.Fatalf("doc = %q, want %q (stored insert X must stay left of Y)", got, "aXYd")
	}
}

// A no-op submit advances the site's seq but not rev.
func TestNoopAdvancesSeqNotRev(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("ab")))
	// Pure retain is a no-op.
	res := mustSubmit(t, d, "s", 1, 1, op(Retain(2)))
	wantResult(t, res, 1, false, op(Retain(2)))
	if d.Rev() != 1 {
		t.Fatalf("rev = %d, want 1", d.Rev())
	}
	// seq=1 was consumed: resubmitting it returns the recorded no-op result.
	dup := mustSubmit(t, d, "s", 1, 1, op(Retain(2)))
	wantResult(t, dup, 1, false, op(Retain(2)))
	// seq=2 is the next expected sequence number.
	if _, err := d.Submit("s", 3, 1, op(Retain(2))); !errors.Is(err, ErrSeqGap) {
		t.Fatalf("seq=3 after noop: got %v, want ErrSeqGap", err)
	}
	mustSubmit(t, d, "s", 2, 1, op(Retain(2)))
}

// A duplicate submit (seq == last accepted) returns the recorded result
// verbatim and changes no state, even across compaction.
func TestDuplicateReturnsRecordedResult(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("ab")))
	res := mustSubmit(t, d, "s", 1, 1, op(Retain(1), Insert("P"), Retain(1)))
	wantResult(t, res, 2, true, op(Retain(1), Insert("P"), Retain(1)))

	// Mutating the returned op must not corrupt the recorded result.
	res.Op[1] = Insert("HACKED")
	dup := mustSubmit(t, d, "s", 1, 1, op(Retain(1), Insert("P"), Retain(1)))
	wantResult(t, dup, 2, true, op(Retain(1), Insert("P"), Retain(1)))
	if d.Rev() != 2 || d.Doc() != "aPb" {
		t.Fatalf("duplicate changed state: rev=%d doc=%q", d.Rev(), d.Doc())
	}

	// Duplicate is still served after compaction drops the base revision.
	if err := d.Compact(2); err != nil {
		t.Fatalf("Compact(2): %v", err)
	}
	dup2 := mustSubmit(t, d, "s", 1, 0, op(Insert("zz")))
	wantResult(t, dup2, 2, true, op(Retain(1), Insert("P"), Retain(1)))
	if d.Rev() != 2 || d.Doc() != "aPb" {
		t.Fatalf("duplicate after compact changed state: rev=%d doc=%q", d.Rev(), d.Doc())
	}
}

func TestSeqStaleAndGap(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "s", 1, 0, op(Insert("ab")))
	mustSubmit(t, d, "s", 2, 1, op(Retain(2)))
	if _, err := d.Submit("s", 1, 1, op(Retain(2))); !errors.Is(err, ErrStaleSeq) {
		t.Fatalf("seq=1: got %v, want ErrStaleSeq", err)
	}
	if _, err := d.Submit("s", 4, 1, op(Retain(2))); !errors.Is(err, ErrSeqGap) {
		t.Fatalf("seq=4: got %v, want ErrSeqGap", err)
	}
	// A rejected submit does not consume the sequence number.
	mustSubmit(t, d, "s", 3, 1, op(Retain(2)))
	// Sites track sequences independently.
	mustSubmit(t, d, "other", 1, 1, op(Retain(2)))
}

// Rejections follow a fixed priority: invalid arguments > sequence class
// (duplicate result, ErrStaleSeq, ErrSeqGap) > ErrFuture > ErrTooOld >
// ErrLength > ErrTooLarge.
func TestErrorPriority(t *testing.T) {
	d := mustNew(t, 3)
	mustSubmit(t, d, "s", 1, 0, op(Insert("ab")))

	// Invalid arguments beat everything, including the duplicate fast path.
	if _, err := d.Submit("", 1, 0, op(Retain(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty site: got %v, want ErrInvalid", err)
	}
	if _, err := d.Submit("s", 0, 0, op(Retain(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("seq=0: got %v, want ErrInvalid", err)
	}
	if _, err := d.Submit("s", 1, -1, op(Retain(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("baseRev=-1: got %v, want ErrInvalid", err)
	}
	if _, err := d.Submit("s", 1, 1, op()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty op: got %v, want ErrInvalid", err)
	}
	if _, err := d.Submit("s", 1, 1, op(Retain(0))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Retain(0): got %v, want ErrInvalid", err)
	}
	if _, err := d.Submit("s", 1, 1, op(Insert(""))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Insert(\"\"): got %v, want ErrInvalid", err)
	}
	if _, err := d.Submit("s", 1, 1, op(Insert("\xff"))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid UTF-8: got %v, want ErrInvalid", err)
	}
	// Duplicate seq returns the recorded result without further validation
	// (baseRev is in the future here, which would otherwise be ErrFuture).
	dup := mustSubmit(t, d, "s", 1, 99, op(Retain(1)))
	wantResult(t, dup, 1, true, op(Insert("ab")))
	// Stale seq beats ErrFuture.
	if _, err := d.Submit("s", 0+1, 99, op(Retain(1))); err != nil {
		t.Fatalf("duplicate with future baseRev must not error, got %v", err)
	}
	mustSubmit(t, d, "s", 2, 1, op(Retain(2)))
	if _, err := d.Submit("s", 1, 99, op(Retain(1))); !errors.Is(err, ErrStaleSeq) {
		t.Fatalf("stale seq + future baseRev: got %v, want ErrStaleSeq", err)
	}
	// Gap beats ErrFuture.
	if _, err := d.Submit("s", 5, 99, op(Retain(1))); !errors.Is(err, ErrSeqGap) {
		t.Fatalf("gap + future baseRev: got %v, want ErrSeqGap", err)
	}
	// ErrFuture beats ErrTooOld/ErrLength/ErrTooLarge.
	if _, err := d.Submit("s", 3, 99, op(Retain(7))); !errors.Is(err, ErrFuture) {
		t.Fatalf("future baseRev: got %v, want ErrFuture", err)
	}
	// ErrLength beats ErrTooLarge: op base length 3 != doc length 2, and the
	// op would also exceed MaxLen.
	if _, err := d.Submit("s", 3, 1, op(Retain(1), Insert("xyz"), Retain(2))); !errors.Is(err, ErrLength) {
		t.Fatalf("length mismatch + too large: got %v, want ErrLength", err)
	}
	// ErrTooLarge is reported last.
	if _, err := d.Submit("s", 3, 1, op(Retain(1), Insert("xy"), Retain(1))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: got %v, want ErrTooLarge", err)
	}
	// Nothing was consumed or changed by the rejections above.
	if d.Rev() != 1 || d.Doc() != "ab" {
		t.Fatalf("rejections changed state: rev=%d doc=%q", d.Rev(), d.Doc())
	}
	mustSubmit(t, d, "s", 3, 1, op(Retain(2)))
}

func TestErrTooOldAndFuture(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("abcd")))
	mustSubmit(t, d, "a", 2, 1, op(Retain(1), Insert("X"), Retain(3)))
	if err := d.Compact(1); err != nil {
		t.Fatalf("Compact(1): %v", err)
	}
	if _, err := d.Submit("s", 1, 0, op(Retain(4))); !errors.Is(err, ErrTooOld) {
		t.Fatalf("baseRev below floor: got %v, want ErrTooOld", err)
	}
	if _, err := d.Submit("s", 1, 3, op(Retain(1))); !errors.Is(err, ErrFuture) {
		t.Fatalf("baseRev above rev: got %v, want ErrFuture", err)
	}
	// baseRev == floor remains valid.
	mustSubmit(t, d, "s", 1, 1, op(Retain(4)))
}

// MaxLen: exact fit passes, one rune more is rejected.
func TestMaxLenExactAndOverflow(t *testing.T) {
	d := mustNew(t, 3)
	mustSubmit(t, d, "s", 1, 0, op(Insert("abc"))) // exactly MaxLen
	if _, err := d.Submit("s", 2, 1, op(Retain(3), Insert("x"))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one rune over MaxLen: got %v, want ErrTooLarge", err)
	}
	// Replacing one rune keeps the length at MaxLen and passes.
	res := mustSubmit(t, d, "s", 2, 1, op(Delete(1), Insert("x"), Retain(2)))
	wantResult(t, res, 2, true, op(Insert("x"), Delete(1), Retain(2)))
	if got := d.Doc(); got != "xbc" {
		t.Fatalf("doc = %q, want %q", got, "xbc")
	}
}

// The MaxLen check uses the transformed operation, not the original one.
// Case A: the original op would fit (result 3 <= 4) but the transformed op
// overflows (result 5 > 4) and must be rejected.
func TestMaxLenJudgedOnTransformedOp_Reject(t *testing.T) {
	d := mustNew(t, 4)
	mustSubmit(t, d, "a", 1, 0, op(Insert("ab")))
	mustSubmit(t, d, "b", 1, 1, op(Insert("PP"), Retain(2))) // doc "PPab", len 4
	// Original op result would be "Qab" (len 3 <= 4), but transformed it
	// produces len 5 > 4.
	if _, err := d.Submit("c", 1, 1, op(Insert("Q"), Retain(2))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	if d.Rev() != 2 || d.Doc() != "PPab" {
		t.Fatalf("rejection changed state: rev=%d doc=%q", d.Rev(), d.Doc())
	}
	// seq was not consumed by the rejection.
	mustSubmit(t, d, "c", 1, 2, op(Retain(4)))
}

// Case B: the original op would overflow (result 6 > 5) but the transformed
// op fits exactly (result 5 <= 5) and must be accepted.
func TestMaxLenJudgedOnTransformedOp_Accept(t *testing.T) {
	d := mustNew(t, 5)
	mustSubmit(t, d, "a", 1, 0, op(Insert("abcde")))
	mustSubmit(t, d, "b", 1, 1, op(Delete(1), Retain(4))) // doc "bcde", len 4
	// Original op result would be len 6 > 5; transformed it deletes 2 and
	// inserts 3 against the current len-4 document: exactly 5.
	res := mustSubmit(t, d, "c", 1, 1, op(Retain(1), Delete(2), Insert("XYZ"), Retain(2)))
	wantResult(t, res, 3, true, op(Insert("XYZ"), Delete(2), Retain(2)))
	if got := d.Doc(); got != "XYZde" {
		t.Fatalf("doc = %q, want %q", got, "XYZde")
	}
}

func TestFloorBoundaries(t *testing.T) {
	d := mustNew(t, 100)
	mustSubmit(t, d, "a", 1, 0, op(Insert("abcd")))                    // rev1: abcd
	mustSubmit(t, d, "a", 2, 1, op(Retain(1), Insert("X"), Retain(3))) // rev2: aXbcd
	mustSubmit(t, d, "a", 3, 2, op(Delete(1), Retain(4)))              // rev3: Xbcd

	if err := d.Compact(-1); !errors.Is(err, ErrBadFloor) {
		t.Fatalf("Compact(-1): got %v, want ErrBadFloor", err)
	}
	if err := d.Compact(4); !errors.Is(err, ErrBadFloor) {
		t.Fatalf("Compact(4) with rev=3: got %v, want ErrBadFloor", err)
	}
	if err := d.Compact(0); err != nil { // floor <= newFloor <= rev, 0 is fine
		t.Fatalf("Compact(0): %v", err)
	}
	if err := d.Compact(2); err != nil {
		t.Fatalf("Compact(2): %v", err)
	}
	if d.Floor() != 2 {
		t.Fatalf("floor = %d, want 2", d.Floor())
	}

	if _, err := d.History(1, 2); !errors.Is(err, ErrTooOld) {
		t.Fatalf("History(1,2): got %v, want ErrTooOld", err)
	}
	// from > to is reported before to > rev.
	if _, err := d.History(3, 2); !errors.Is(err, ErrBadRange) {
		t.Fatalf("History(3,2): got %v, want ErrBadRange", err)
	}
	if _, err := d.History(2, 4); !errors.Is(err, ErrBadRange) {
		t.Fatalf("History(2,4): got %v, want ErrBadRange", err)
	}
	h, err := d.History(2, 3)
	if err != nil {
		t.Fatalf("History(2,3): %v", err)
	}
	if !reflect.DeepEqual(h, []Operation{op(Delete(1), Retain(4))}) {
		t.Fatalf("History(2,3) = %v", h)
	}

	// baseRev == floor is still accepted; baseRev == floor-1 is too old.
	if _, err := d.Submit("s", 1, 1, op(Retain(4))); !errors.Is(err, ErrTooOld) {
		t.Fatalf("baseRev=floor-1: got %v, want ErrTooOld", err)
	}
	mustSubmit(t, d, "s", 1, 2, op(Retain(5)))

	// Compacting to the current floor and to rev are both legal.
	if err := d.Compact(2); err != nil {
		t.Fatalf("Compact(2) again: %v", err)
	}
	if err := d.Compact(3); err != nil {
		t.Fatalf("Compact(3)=rev: %v", err)
	}
	h, err = d.History(3, 3)
	if err != nil || len(h) != 0 {
		t.Fatalf("History(3,3) = %v, %v; want empty", h, err)
	}
}

// Stored history and returned operations are canonical, and callers cannot
// mutate server state through returned or submitted slices.
func TestHistoryCanonicalAndIsolated(t *testing.T) {
	d := mustNew(t, 100)
	// Non-canonical input: adjacent retains and a Delete before an Insert.
	in := op(Retain(1), Retain(1), Delete(1), Insert("x"))
	mustSubmit(t, d, "s", 1, 0, op(Insert("abc")))
	res := mustSubmit(t, d, "s", 2, 1, in)
	want := op(Retain(2), Insert("x"), Delete(1))
	wantResult(t, res, 2, true, want)

	h, err := d.History(0, 2)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if !reflect.DeepEqual(h[1], want) {
		t.Fatalf("stored op = %v, want canonical %v", h[1], want)
	}
	// Mutating the submitted input must not affect stored history.
	in[3] = Insert("HACKED")
	// Mutating returned slices must not affect stored history either.
	h[1][0] = Delete(9)
	res.Op[0] = Delete(9)
	h2, _ := d.History(1, 2)
	if !reflect.DeepEqual(h2[0], want) {
		t.Fatalf("stored history mutated: %v", h2[0])
	}
	if got := d.Doc(); got != "abx" {
		t.Fatalf("doc = %q, want %q", got, "abx")
	}
}

// Multibyte runes count as single characters for lengths and MaxLen.
func TestUTF8Runes(t *testing.T) {
	d := mustNew(t, 4)
	mustSubmit(t, d, "s", 1, 0, op(Insert("hé界🙂"))) // 4 runes, 11 bytes
	if got := d.Len(); got != 4 {
		t.Fatalf("len = %d, want 4 runes", got)
	}
	if _, err := d.Submit("s", 2, 1, op(Retain(4), Insert("!"))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("5th rune: got %v, want ErrTooLarge", err)
	}
	res := mustSubmit(t, d, "s", 2, 1, op(Retain(1), Delete(3), Insert("好")))
	wantResult(t, res, 2, true, op(Retain(1), Insert("好"), Delete(3)))
	if got := d.Doc(); got != "h好" {
		t.Fatalf("doc = %q, want %q", got, "h好")
	}
}

// For a single stored operation the transform never takes more than
// len(c)+len(s) component-processing steps.
func TestTransformStepBound(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 2000; trial++ {
		base := 1 + rng.Intn(12)
		doc := randRunes(rng, base)
		c := Normalize(randOp(rng, base))
		s := Normalize(randOp(rng, base))
		cs, steps := transform(c, s)
		if steps > len(c)+len(s) {
			t.Fatalf("trial %d: steps=%d > len(c)+len(s)=%d+%d\nc=%v\ns=%v",
				trial, steps, len(c), len(s), c, s)
		}
		// The transformed operation applies cleanly to the post-s document.
		post := apply(doc, s)
		if got := len(apply(post, cs)); got != len(post)+insertRunes(cs)-deleteRunes(cs) {
			t.Fatalf("trial %d: transformed op does not apply cleanly", trial)
		}
	}
}
