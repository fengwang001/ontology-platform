package frame

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/lenp"
)

// refEncode is the textbook implementation used as the byte-level oracle.
func refEncode(fields [][]byte) []byte {
	out := make([]byte, 0)
	for _, f := range fields {
		n := len(f)
		out = append(out, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
		out = append(out, f...)
	}
	return out
}

// TestRoundTrip pins invariant 1: Encode then Decode is byte-identical per
// field, including empty fields. Table-driven, random cases generated in loop.
func TestRoundTrip(t *testing.T) {
	cases := [][][]byte{
		{},
		{[]byte("hi"), nil, []byte("world!"), []byte("A")},
		{nil, []byte{}, nil},
		{make([]byte, 0)},
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		f := make([][]byte, rng.Intn(20))
		for j := range f {
			b := make([]byte, rng.Intn(300))
			rng.Read(b)
			f[j] = b
		}
		cases = append(cases, f)
	}
	for _, fields := range cases {
		got, err := Decode(Encode(fields))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(got) != len(fields) {
			t.Fatalf("field count: got %d want %d", len(got), len(fields))
		}
		for i := range fields {
			if !bytes.Equal(got[i], fields[i]) {
				t.Fatalf("field %d mismatch", i)
			}
		}
	}
}

// TestPrefixSelfConsistent pins invariant 2: prefix value == payload length,
// no overlap, no gap, whole buffer covered.
func TestPrefixSelfConsistent(t *testing.T) {
	fields := [][]byte{[]byte("hi"), {}, []byte("world!"), []byte("A")}
	buf := Encode(fields)
	r := NewReader(buf)
	for r.Pos() < len(buf) {
		start := r.Pos()
		n, err := lenp.GetLength(buf[start:])
		if err != nil {
			t.Fatal(err)
		}
		f, err := r.NextField()
		if err != nil {
			t.Fatalf("NextField: %v", err)
		}
		if len(f) != n {
			t.Fatalf("prefix %d != payload %d", n, len(f))
		}
		if r.Pos() != start+4+n {
			t.Fatalf("overlap/gap at %d", start)
		}
	}
	if r.Pos() != len(buf) {
		t.Fatalf("coverage: pos %d len %d", r.Pos(), len(buf))
	}
}

// TestEncodeMatchesReference pins invariant 3.
func TestEncodeMatchesReference(t *testing.T) {
	cases := [][][]byte{
		{[]byte("hi"), nil, []byte("world!"), []byte("A")},
		{{}, make([]byte, 256), []byte("z")},
	}
	for _, c := range cases {
		if !bytes.Equal(Encode(c), refEncode(c)) {
			t.Fatal("Encode diverges from textbook reference")
		}
	}
}

// TestAtomicFailure pins invariant 4: rejected decode returns (nil, error),
// truncated and illegal prefixes are distinct sentinels.
func TestAtomicFailure(t *testing.T) {
	good := Encode([][]byte{[]byte("hi"), []byte("xy")})
	trunc := append(append([]byte{}, good...), 100, 0, 0, 0) // says 100, 0 left
	if f, err := Decode(trunc); !errors.Is(err, ErrTruncated) || f != nil {
		t.Fatalf("truncated: f=%v err=%v", f, err)
	}
	illegal := append(append([]byte{}, good...), 0x01, 0x02) // 2-byte tail
	if f, err := Decode(illegal); !errors.Is(err, ErrIllegalPrefix) || f != nil {
		t.Fatalf("illegal: f=%v err=%v", f, err)
	}
}

// TestSkipFailureNoAdvance: a failed SkipField/NextField keeps Pos, and the
// reader stays usable on a valid record afterwards.
func TestSkipFailureNoAdvance(t *testing.T) {
	good := Encode([][]byte{[]byte("hi"), []byte("xy")})
	for _, bad := range [][]byte{
		append(append([]byte{}, good...), 100, 0, 0, 0),
		append(append([]byte{}, good...), 0x01, 0x02),
	} {
		r := NewReader(bad)
		for r.Pos() < len(bad) {
			p := r.Pos()
			if err := r.SkipField(); err == nil {
				continue
			} else if r.Pos() != p {
				t.Fatalf("failed skip advanced %d -> %d", p, r.Pos())
			}
			if _, err := r.NextField(); err == nil || r.Pos() != p {
				t.Fatal("reader not frozen on failure")
			}
			break
		}
	}
	ok := NewReader(good)
	if err := ok.SkipField(); err != nil || ok.Pos() != 6 {
		t.Fatal("reader unusable after prior failures")
	}
}
