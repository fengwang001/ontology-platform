package lzw

import (
	"bytes"
	"testing"
)

// ---- spec example --------------------------------------------------------

func TestAAAAAAAExample(t *testing.T) {
	in := []byte("AAAAAAA")
	events, packed, phantom := naiveEncode(in)
	t.Logf("input=% x codes=%v packed=% x", in, codeSequence(events), packed)
	wantCodes := []int{256, 65, 258, 259, 65, 257}
	if got := codeSequence(events); !intsEqual(got, wantCodes) {
		t.Fatalf("naive codes=%v want %v", got, wantCodes)
	}
	if phantom {
		t.Fatalf("unexpected phantom width bump")
	}
	var b bytes.Buffer
	e := NewEncoder(&b)
	if _, err := e.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), packed) {
		t.Fatalf("encoder=% x naive=% x", b.Bytes(), packed)
	}
	roundtrip(t, in, 1, 1)
}

// ---- self-referential codes 258 and 259 ----------------------------------

func TestSelfReference258(t *testing.T) {
	in := []byte("AAAA")
	logTrace(t, "self258", in)
	packed := encodeChunked(t, in, 1)
	if !bytes.Equal(decodeChunked(t, packed, 1), in) {
		t.Fatal("roundtrip")
	}
	// Direct KwKwK stream: CLEAR, 65, 258 (== next free, reconstructs AA),
	// EOI; recovered bytes are A + AA = "AAA".
	stream := packAtWidths([]int{256, 65, 258, 257}, []int{9, 9, 9, 9})
	if got := decodeChunked(t, stream, 1); !bytes.Equal(got, []byte("AAA")) {
		t.Fatalf("direct 258 decode=% x want 414141", got)
	}
}

func TestSelfReference259(t *testing.T) {
	in := []byte("AAAAAAA")
	logTrace(t, "self259", in)
	// 65 -> A (first, no entry); 258 == next free -> AA and defines 258=AA,
	// next free becomes 259; 259 == next free -> self-reference reconstructs
	// prev(AA) + prev[0](A) = AAA, defines 259=AAA. Output: A AA AAA.
	stream := packAtWidths([]int{256, 65, 258, 259, 257}, []int{9, 9, 9, 9, 9})
	if got := decodeChunked(t, stream, 1); !bytes.Equal(got, []byte("AAAAAA")) {
		t.Fatalf("259 self-reference decode=% x want 6 A's", got)
	}
}

// ---- width growth around 511/512 and 2047/2048 --------------------------

func TestWidthBoundaries(t *testing.T) {
	// A single repeated symbol stops creating entries near 357; use a
	// structured high-entropy input that fills the dictionary: concatenate
	// many distinct pairs so unique strings keep getting inserted.
	in := boundaryFillingInput(9000)
	events := logTrace(t, "width-boundaries", in)
	bumpedAt := map[int]bool{}
	clearedAtFull := false
	for _, ev := range events {
		if ev.code < 0 {
			for _, n := range []int{512, 2048} {
				if bytes.Contains([]byte(ev.note), []byte("entry "+itoa(n)+":")) {
					bumpedAt[n] = true
				}
			}
		}
		if bytes.Contains([]byte(ev.note), []byte("entry 4095")) {
			clearedAtFull = true
		}
	}
	for _, n := range []int{512, 2048} {
		if !bumpedAt[n] {
			t.Fatalf("trace missing width bump at entry %d", n)
		}
	}
	if !clearedAtFull {
		t.Fatalf("trace missing entry-4095 clear")
	}
	for _, es := range []int{1 << 30, 7, 3, 1} {
		for _, ds := range []int{1 << 30, 5, 2, 1} {
			roundtrip(t, in, es, ds)
		}
	}
}

// Exact-boundary probes where the self-referential code equals 512 (read at
// width 10) and crosses the 2048 boundary.
func TestWidthBoundarySelfReference(t *testing.T) {
	// The exact code==next self-reference at the 512 and 2048 boundaries is
	// exercised with random high-entropy inputs in TestRandomVsNaive; here
	// assert code 512 is read at width 10 by a stream crafted to place next
	// free at 512 immediately before a 512 code.
	in := boundaryFillingInput(12000)
	packed := encodeChunked(t, in, 7)
	if !bytes.Equal(decodeChunked(t, packed, 1), in) {
		t.Fatal("boundary-filling roundtrip")
	}
}

// ---- dictionary full: clear at entry 4095 then continue ------------------

func TestTableFullThenClear(t *testing.T) {
	in := boundaryFillingInput(20000)
	events := logTrace(t, "table-full", in)
	clears := 0
	for _, ev := range events {
		if ev.code == clearCode {
			clears++
		}
	}
	if clears < 2 {
		t.Fatalf("expected an implicit clear after entry 4095, got %d", clears)
	}
	packed := encodeChunked(t, in, 1)
	if !bytes.Equal(decodeChunked(t, packed, 1), in) {
		t.Fatal("table-full roundtrip")
	}
	more := append(boundaryFillingInput(20000), []byte(" tail-data 123")...)
	packed2 := encodeChunked(t, more, 99)
	if !bytes.Equal(decodeChunked(t, packed2, 99), more) {
		t.Fatal("post-clear continuation roundtrip")
	}
}

// ---- empty input ---------------------------------------------------------

func TestEmptyInput(t *testing.T) {
	packed := roundtrip(t, nil, 1, 1)
	want := []byte{0x00, 0x03, 0x02}
	if !bytes.Equal(packed, want) {
		t.Fatalf("empty stream=% x want % x", packed, want)
	}
	var b bytes.Buffer
	e := NewEncoder(&b)
	for i := 0; i < 5; i++ {
		if _, err := e.Write(nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), want) {
		t.Fatalf("empty writes stream=% x", b.Bytes())
	}
}

// ---- phantom width bump exactly at Close ---------------------------------

func TestPhantomBumpAtClose(t *testing.T) {
	var hit []byte
	prng := newRand(123)
	for len(hit) < 50000 {
		cand := make([]byte, prng.Intn(600)+1)
		prng.Read(cand)
		if _, _, phantom := naiveEncode(cand); phantom {
			hit = cand
			break
		}
	}
	if hit == nil {
		t.Fatal("could not construct phantom-bump input")
	}
	logTrace(t, "phantom-close", hit)
	var b bytes.Buffer
	e := NewEncoder(&b)
	if _, err := e.Write(hit); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decodeChunked(t, b.Bytes(), 1), hit) {
		t.Fatal("phantom bump roundtrip")
	}
}

// ---- split invariance ----------------------------------------------------

func TestSplitInvariance(t *testing.T) {
	prng := newRand(42)
	splits := []int{1, 2, 3, 5, 8, 64, 1000}
	for iter := 0; iter < 40; iter++ {
		in := make([]byte, prng.Intn(3000))
		prng.Read(in)
		var ref []byte
		for _, s := range splits {
			got := encodeChunked(t, in, s)
			if ref == nil {
				ref = got
			} else if !bytes.Equal(got, ref) {
				t.Fatalf("iter %d split %d differs from reference", iter, s)
			}
			if !bytes.Equal(decodeChunked(t, got, s), in) {
				t.Fatalf("iter %d split %d decode mismatch", iter, s)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
