package serve

import (
	"bytes"
	"errors"
	"ontology/source"
	"testing"
)

// assertZeroState checks that a rejected Assemble left no trace.
func assertZeroState(t *testing.T, a *Assembler) {
	t.Helper()
	if a.Ranges() != nil || a.Total() != 0 || a.Written() != 0 ||
		a.IsMultipart() || a.Boundary() != "" {
		t.Fatalf("rejection mutated state: ranges=%v total=%d written=%d multi=%v boundary=%q",
			a.Ranges(), a.Total(), a.Written(), a.IsMultipart(), a.Boundary())
	}
}

func TestTooManyRanges(t *testing.T) {
	a := NewAssembler(Config{MaxRanges: 2}, source.Bytes([]byte("0123456789")))
	err := a.Assemble("bytes=0-1, 2-3, 4-5")
	if !errors.Is(err, ErrTooManyRanges) {
		t.Fatalf("want ErrTooManyRanges, got %v", err)
	}
	assertZeroState(t, a)
}

func TestResponseTooLarge(t *testing.T) {
	data := source.Bytes([]byte("0123456789"))
	a := NewAssembler(Config{MaxBytes: 5}, data)
	err := a.Assemble("bytes=0-9")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("want ErrResponseTooLarge, got %v", err)
	}
	assertZeroState(t, a)
	// Multipart framing bytes count toward the limit too.
	b := NewAssembler(Config{MaxBytes: 30, Rand: fixedRand()}, data)
	if err := b.Assemble("bytes=0-1, 8-9"); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("multipart over limit: got %v", err)
	}
	assertZeroState(t, b)
}

// collidingRand always produces the same 16 bytes (repeated so several
// attempts can read), so the generated boundary always collides with a
// payload crafted to contain it.
func collidingRand() *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte("0123456789abcdef"), 8))
}

func TestBoundaryRetriesExhausted(t *testing.T) {
	// The payload contains the exact token every attempt will generate.
	token := "ontology-30313233343536373839616263646566" // hex of "0123456789abcdef"
	data := source.Bytes([]byte(token + "PAD" + token))
	a := NewAssembler(Config{MaxBoundaryTries: 3, Rand: collidingRand()}, data)
	err := a.Assemble("bytes=0-40, 44-84") // two non-adjacent ranges, each a full token
	if !errors.Is(err, ErrBoundaryExhausted) {
		t.Fatalf("want ErrBoundaryExhausted, got %v", err)
	}
	assertZeroState(t, a)
}

func TestLimitErrorsAreMutuallyDistinguishable(t *testing.T) {
	errs := []error{ErrTooManyRanges, ErrResponseTooLarge, ErrBoundaryExhausted}
	for i, e := range errs {
		for j, other := range errs {
			if i != j && errors.Is(e, other) {
				t.Fatalf("errors %d and %d are not distinguishable", i, j)
			}
		}
	}
}

// TestRejectionKeepsPriorState: a failed re-assembly must not clobber a
// previously assembled response.
func TestRejectionKeepsPriorState(t *testing.T) {
	data := source.Bytes([]byte("0123456789"))
	a := NewAssembler(Config{MaxRanges: 1}, data)
	if err := a.Assemble("bytes=0-3"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteTo(&bytes.Buffer{}); err != nil { // partially consume
		t.Fatal(err)
	}
	if err := a.Assemble("bytes=0-1, 2-3"); !errors.Is(err, ErrTooManyRanges) {
		t.Fatalf("want ErrTooManyRanges, got %v", err)
	}
	rs := a.Ranges()
	if a.Total() != 4 || a.Written() != 4 || a.IsMultipart() ||
		len(rs) != 1 || rs[0].From != 0 || rs[0].To != 3 {
		t.Fatalf("rejection clobbered state: total=%d written=%d ranges=%v",
			a.Total(), a.Written(), rs)
	}
}
