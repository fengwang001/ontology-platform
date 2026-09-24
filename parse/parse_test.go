package parse

import (
	"errors"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ontology/fmtf"
)

func sigCount(s string) int {
	if i := strings.IndexByte(s, 'e'); i >= 0 {
		s = s[:i]
	}
	s = strings.NewReplacer("-", "", ".", "").Replace(s)
	s = strings.TrimRight(strings.TrimLeft(s, "0"), "0")
	return len(s)
}

func roundTrip(t *testing.T, f float64) string {
	t.Helper()
	s, err := fmtf.Encode(f)
	if err != nil {
		t.Fatalf("Encode(%v): %v", f, err)
	}
	g, err := Float(s)
	if err != nil {
		t.Fatalf("Float(%q): %v", s, err)
	}
	if math.Float64bits(g) != math.Float64bits(f) {
		t.Fatalf("round-trip %v -> %q -> %v", f, s, g)
	}
	return s
}

func TestRoundTrip100k(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	longer := 0
	for i := 0; i < 100000; i++ {
		f := math.Float64frombits(rng.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		s, err := fmtf.Encode(f)
		if err != nil {
			t.Fatalf("Encode(%v): %v", f, err)
		}
		g, err := Float(s)
		if err != nil || math.Float64bits(g) != math.Float64bits(f) {
			t.Fatalf("round-trip failed: %v -> %q -> %v (err %v)", f, s, g, err)
		}
		if f == 0 {
			continue
		}
		ref := strconv.FormatFloat(f, 'g', -1, 64)
		if sigCount(s) > sigCount(ref) {
			longer++
		}
	}
	if longer > 0 {
		t.Fatalf("%d values longer than strconv shortest", longer)
	}
}

func TestSubnormals(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	seen := map[uint64]bool{1: true, 2: true, 3: true, 1 << 20: true, 1<<52 - 1: true}
	for len(seen) < 200 {
		seen[rng.Uint64()&(1<<52-1)] = true
	}
	for b := range seen {
		roundTrip(t, math.Float64frombits(b))
	}
}

func TestExtremes(t *testing.T) {
	cases := []struct {
		name string
		f    float64
		want string
	}{
		{"max", math.MaxFloat64, "1.7976931348623157e+308"},
		{"min subnormal", math.SmallestNonzeroFloat64, "5e-324"},
		{"2^53-1", 1<<53 - 1, "9007199254740991"},
		{"2^53", 1 << 53, "9007199254740992"},
		{"2^53+2", 1<<53 + 2, "9007199254740994"},
	}
	for _, c := range cases {
		if s := roundTrip(t, c.f); s != c.want {
			t.Errorf("%s: got %q, want %q", c.name, s, c.want)
		}
	}
}

func TestSignedZeros(t *testing.T) {
	pos := roundTrip(t, 0.0)
	neg := roundTrip(t, math.Copysign(0, -1))
	if pos == neg {
		t.Fatalf("+0 and -0 both encode as %q", pos)
	}
	g, _ := Float(neg)
	if !math.Signbit(g) {
		t.Fatal("-0 lost its sign")
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrEmpty},
		{"bad char", "12a3", ErrInvalidChar},
		{"double dot", "1.2.3", ErrInvalidChar},
		{"no digits", "-", ErrInvalidChar},
		{"empty exp", "1e", ErrInvalidChar},
		{"exp overflow pos", "1e+500", ErrExpOverflow},
		{"exp overflow neg", "1e-500", ErrExpOverflow},
		{"exp huge", "1e99999999999999999999", ErrExpOverflow},
		{"too many digits", "1.00000000000000001", ErrTooManyDigits},
	}
	for _, c := range cases {
		_, err := Float(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: Float(%q) err = %v, want %v", c.name, c.in, err, c.want)
		}
	}
	_, err := Float("12a3")
	if err == nil || !strings.Contains(err.Error(), "byte 2") {
		t.Errorf("invalid char error should carry byte position, got %v", err)
	}
}

func TestConcurrent(t *testing.T) {
	vals := []float64{0.1, 1.0 / 3.0, math.MaxFloat64, math.SmallestNonzeroFloat64, -2.5, 1e21}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				f := vals[(w+i)%len(vals)]
				s, err := fmtf.Encode(f)
				if err != nil {
					t.Error(err)
					return
				}
				g, err := Float(s)
				if err != nil || math.Float64bits(g) != math.Float64bits(f) {
					t.Errorf("concurrent round-trip failed: %v %q", f, s)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
