package lzw

import (
	"bytes"
	"testing"
)

// boundaryFillingInput returns a deterministic pseudo-random byte sequence
// that keeps introducing novel strings, driving the dictionary through the
// 512/2048 width bumps and the 4095 forced clear when n is large enough.
func boundaryFillingInput(n int) []byte {
	r := newRand(2024)
	b := make([]byte, n)
	// Mix fresh randomness with occasional short repeats: fully random input
	// inserts on nearly every code, reaching entry 4095 in roughly 3840 codes.
	for i := range b {
		switch r.Intn(4) {
		case 0:
			b[i] = byte(r.Intn(256))
		case 1:
			b[i] = byte(i * 31)
		default:
			b[i] = byte(r.Intn(16))
		}
	}
	return b
}

func encodeChunked(t *testing.T, in []byte, chunk int) []byte {
	t.Helper()
	var b bytes.Buffer
	e := NewEncoder(&b)
	for i := 0; i < len(in); i += chunk {
		end := i + chunk
		if end > len(in) {
			end = len(in)
		}
		n, err := e.Write(in[i:end])
		if err != nil || n != end-i {
			t.Fatalf("Write(%d:%d)=%d,%v", i, end, n, err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return b.Bytes()
}

func decodeChunked(t *testing.T, packed []byte, chunk int) []byte {
	t.Helper()
	var b bytes.Buffer
	d := NewDecoder(&b)
	for i := 0; i < len(packed); i += chunk {
		end := i + chunk
		if end > len(packed) {
			end = len(packed)
		}
		if n, err := d.Write(packed[i:end]); err != nil {
			t.Fatalf("decoder Write(%d:%d)=%d,%v", i, end, n, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatalf("decoder Close: %v", err)
	}
	return b.Bytes()
}

func roundtrip(t *testing.T, in []byte, encSplit, decSplit int) []byte {
	t.Helper()
	packed := encodeChunked(t, in, encSplit)
	out := decodeChunked(t, packed, decSplit)
	if !bytes.Equal(out, in) {
		t.Fatalf("roundtrip mismatch: inLen=%d outLen=%d", len(in), len(out))
	}
	return packed
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// logTrace prints input, naive code sequence and every width/clear decision.
func logTrace(t *testing.T, label string, in []byte) []naiveEvent {
	t.Helper()
	events, packed, phantom := naiveEncode(in)
	t.Logf("TRACE %s: inputLen=%d input=% x", label, len(in), truncate(in, 64))
	t.Logf("TRACE %s: code sequence=%v", label, codeSequence(events))
	t.Logf("TRACE %s: packedLen=%d packed=% x phantomAtClose=%v",
		label, len(packed), truncate(packed, 64), phantom)
	for _, ev := range events {
		if ev.code < 0 || ev.code == clearCode || ev.code == endCode {
			t.Logf("TRACE %s:   decision: %s", label, ev.note)
		}
	}
	return events
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return append(append([]byte(nil), b[:n]...), '.', '.', '.')
}

// packAtWidths packs each code with the explicit width at that position.
func packAtWidths(codes, widths []int) []byte {
	var out []byte
	acc := uint32(0)
	nbits := 0
	for i, code := range codes {
		w := widths[i]
		acc |= uint32(code) << nbits
		nbits += w
		for nbits >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			nbits -= 8
		}
	}
	if nbits > 0 {
		out = append(out, byte(acc))
	}
	return out
}
