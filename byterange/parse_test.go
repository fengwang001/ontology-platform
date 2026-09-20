package byterange

import (
	"errors"
	"reflect"
	"testing"
)

func mustParse(t *testing.T, header string, size int64) []Range {
	t.Helper()
	got, err := Parse(header, size)
	if err != nil {
		t.Fatalf("Parse(%q, %d) unexpected error: %v", header, size, err)
	}
	return got
}

func checkRanges(t *testing.T, header string, size int64, want []Range) {
	t.Helper()
	got := mustParse(t, header, size)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(%q, %d) = %v, want %v", header, size, got, want)
	}
}

// Semantics 1: the three range-spec forms all normalize to absolute
// closed intervals.
func TestThreeForms(t *testing.T) {
	checkRanges(t, "bytes=0-499", 1000, []Range{{0, 499}})
	checkRanges(t, "bytes=500-", 1000, []Range{{500, 999}})
	checkRanges(t, "bytes=-500", 1000, []Range{{500, 999}})
	checkRanges(t, "bytes=0-0", 1000, []Range{{0, 0}}) // first byte
}

// Semantics 2: out-of-range ends are clipped, never an error.
func TestClipping(t *testing.T) {
	checkRanges(t, "bytes=0-999999", 1000, []Range{{0, 999}})
	checkRanges(t, "bytes=-999999", 1000, []Range{{0, 999}})
	checkRanges(t, "bytes=950-999999", 1000, []Range{{950, 999}})
}

// Semantics 3: unsatisfiable specs are skipped; only when none are
// satisfiable is ErrUnsatisfiable returned. "-0" is unsatisfiable.
func TestUnsatisfiable(t *testing.T) {
	checkRanges(t, "bytes=1000-, 0-9", 1000, []Range{{0, 9}})
	checkRanges(t, "bytes=-0, 10-19", 1000, []Range{{10, 19}})

	for _, h := range []string{"bytes=1000-", "bytes=-0", "bytes=5000-6000"} {
		got, err := Parse(h, 1000)
		if !errors.Is(err, ErrUnsatisfiable) {
			t.Fatalf("Parse(%q, 1000) err = %v, want ErrUnsatisfiable", h, err)
		}
		if got != nil {
			t.Fatalf("Parse(%q, 1000) = %v, want nil slice", h, got)
		}
	}
}

// Semantics 4: size 0 makes everything unsatisfiable.
func TestZeroSize(t *testing.T) {
	for _, h := range []string{"bytes=0-", "bytes=0-0", "bytes=-1"} {
		got, err := Parse(h, 0)
		if !errors.Is(err, ErrUnsatisfiable) {
			t.Fatalf("Parse(%q, 0) err = %v, want ErrUnsatisfiable", h, err)
		}
		if got != nil {
			t.Fatalf("Parse(%q, 0) = %v, want nil slice", h, got)
		}
	}
}

// Semantics 5: overlapping and adjacent ranges merge; output is
// sorted by start, disjoint and reproducible.
func TestMerge(t *testing.T) {
	checkRanges(t, "bytes=0-100, 50-200", 1000, []Range{{0, 200}})
	checkRanges(t, "bytes=0-100, 101-200", 1000, []Range{{0, 200}}) // adjacent
	checkRanges(t, "bytes=500-600, 0-100, 150-160", 1000,
		[]Range{{0, 100}, {150, 160}, {500, 600}}) // sorted, disjoint
	checkRanges(t, "bytes=0-100, 102-200", 1000, []Range{{0, 100}, {102, 200}})
	checkRanges(t, "bytes=-100, 900-", 1000, []Range{{900, 999}}) // same via two forms
}

// Semantics 6: syntax errors are ErrMalformed with a nil slice.
func TestMalformed(t *testing.T) {
	bad := []string{
		"items=0-10",    // wrong unit
		"bytes",         // missing '='
		"bytes=500-100", // start > end
		"bytes=abc-def", // non-numeric
		"bytes=-",       // neither start nor suffix length
		"bytes=",        // empty range list
		"bytes=0-10,",   // empty spec in list
		"bytes=0-10-20", // too many dashes
		"bytes=+5-10",   // sign not allowed
		"bytes=1.5-10",  // not an integer
		"",              // empty header
	}
	for _, h := range bad {
		got, err := Parse(h, 1000)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q, 1000) err = %v, want ErrMalformed", h, err)
		}
		if got != nil {
			t.Errorf("Parse(%q, 1000) = %v, want nil slice", h, got)
		}
	}
	// Malformed wins over unsatisfiable when both appear.
	if _, err := Parse("bytes=1000-, abc", 1000); !errors.Is(err, ErrMalformed) {
		t.Fatalf("mixed malformed/unsatisfiable: err = %v, want ErrMalformed", err)
	}
}

// Semantics 7: values beyond int64 are malformed, never wrapped.
func TestOverflow(t *testing.T) {
	big := "9223372036854775808" // math.MaxInt64 + 1
	for _, h := range []string{
		"bytes=" + big + "-",
		"bytes=0-" + big,
		"bytes=-" + big,
	} {
		if _, err := Parse(h, 1000); !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q, 1000) err = %v, want ErrMalformed", h, err)
		}
	}
	// MaxInt64 itself is fine and clips cleanly.
	checkRanges(t, "bytes=0-9223372036854775807", 1000, []Range{{0, 999}})
}

// Semantics 8: Parse is pure and repeatable.
func TestRepeatable(t *testing.T) {
	header := "bytes=500-600, 0-100, -50"
	first := mustParse(t, header, 1000)
	second := mustParse(t, header, 1000)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated parse differs: %v vs %v", first, second)
	}
	if header != "bytes=500-600, 0-100, -50" {
		t.Fatalf("header mutated: %q", header)
	}
}
