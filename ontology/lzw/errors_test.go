package lzw

import (
	"bytes"
	"errors"
	"testing"
)

func expectDecodeError(t *testing.T, stream []byte, want error, wantIdx int) {
	t.Helper()
	var b bytes.Buffer
	d := NewDecoder(&b)
	_, errW := d.Write(stream)
	errC := d.Close()
	err := errW
	if err == nil {
		err = errC
	}
	if err == nil {
		t.Fatalf("stream % x: expected error, got nil (out=% x)", stream, b.Bytes())
	}
	if !errors.Is(err, want) {
		t.Fatalf("stream % x: error=%v want errors.Is %v", stream, err, want)
	}
	var ce *CodeError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *CodeError", err)
	}
	if ce.CodeIndex != wantIdx {
		t.Fatalf("error %v: code index=%d want %d", err, ce.CodeIndex, wantIdx)
	}
	// Sticky: later calls return the same error and never mutate state.
	outBefore := append([]byte(nil), b.Bytes()...)
	if n, err2 := d.Write([]byte{0, 0, 0}); err2 != err || n != 0 {
		t.Fatalf("post-failure Write=%d,%v (same? %v)", n, err2, errors.Is(err2, want))
	}
	if err3 := d.Close(); err3 != err {
		t.Fatalf("post-failure Close=%v want same error", err3)
	}
	if !bytes.Equal(b.Bytes(), outBefore) {
		t.Fatalf("sticky failure mutated output")
	}
}

// First code is not a clear code: 9-bit code 0 then EOI.
func TestErrFirstCodeNotClear(t *testing.T) {
	stream := packAtWidths([]int{0, 257}, []int{9, 9})
	expectDecodeError(t, stream, ErrFirstCodeNotClear, 1)
}

// First code after clear is >= 256 (here 258, the first free code).
func TestErrFirstAfterClear(t *testing.T) {
	stream := packAtWidths([]int{256, 258, 257}, []int{9, 9, 9})
	expectDecodeError(t, stream, ErrFirstAfterClear, 2)
}

// First code after a later clear must be a literal too.
func TestErrFirstAfterSecondClear(t *testing.T) {
	// CLEAR, 65, 66, 258 (defines ABA... actually AB+A), CLEAR, 300, EOI
	stream := packAtWidths(
		[]int{256, 65, 66, 258, 256, 300, 257},
		[]int{9, 9, 9, 9, 9, 9, 9})
	expectDecodeError(t, stream, ErrFirstAfterClear, 6)
}

// Code strictly greater than the next free number is invalid; equal is legal.
func TestErrInvalidCode(t *testing.T) {
	// After CLEAR,65,66 the decoder has added entry 258; next free is 259.
	// Code 260 > 259 -> invalid. 259 itself (self-reference) would be legal.
	stream := packAtWidths([]int{256, 65, 66, 260, 257}, []int{9, 9, 9, 9, 9})
	expectDecodeError(t, stream, ErrInvalidCode, 4)
}

// Code equal to the next free number must be accepted.
func TestCodeEqualsNextIsLegal(t *testing.T) {
	stream := packAtWidths([]int{256, 65, 66, 259, 257}, []int{9, 9, 9, 9, 9})
	out := decodeChunked(t, stream, 1)
	// A (first), B -> defines 258=AB, 259==next free -> BB (B+B).
	if !bytes.Equal(out, []byte("ABBB")) {
		t.Fatalf("self reference output=% x", out)
	}
}

// At width 12, code 4095 (equal to the next free number at that point) is
// legal; 4096 is outside the width and cannot be sent, but 4095 followed by
// data must decode without ErrInvalidCode.
func TestCode4095Boundary(t *testing.T) {
	// Use a real stream that reaches entry 4095; verify it round-trips.
	in := boundaryFillingInput(20000)
	packed := encodeChunked(t, in, 1)
	out := decodeChunked(t, packed, 1)
	if !bytes.Equal(out, in) {
		t.Fatal("code-4095 region roundtrip")
	}
}

// Non-zero padding bits after EOI (EOI ends mid-byte).
func TestErrPaddingNonZero(t *testing.T) {
	// CLEAR, EOI occupies 18 bits; force the upper 6 bits of the final byte
	// to 1. Good stream is 00 03 02; set padding of byte2 to 0xfc.
	good := packAtWidths([]int{256, 257}, []int{9, 9})
	good[2] |= 0xfc
	expectDecodeError(t, good, ErrPaddingNonZero, 2)
}

// Extra bytes after a byte-aligned EOI.
func TestErrTrailingData(t *testing.T) {
	good := packAtWidths([]int{256, 65, 257}, []int{9, 9, 9})
	good = append(good, 0x01)
	expectDecodeError(t, good, ErrTrailingData, 3)
}

// A separate Write delivering bytes after EOI also reports trailing data.
func TestErrTrailingDataAcrossWrites(t *testing.T) {
	good := packAtWidths([]int{256, 257}, []int{9, 9})
	var b bytes.Buffer
	d := NewDecoder(&b)
	if _, err := d.Write(good); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Write([]byte{0}); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("post-EOI Write err=%v", err)
	}
}

// Truncated before EOI, both mid-code and mid-stream.
func TestErrTruncated(t *testing.T) {
	good := packAtWidths([]int{256, 65, 66, 258, 257}, []int{9, 9, 9, 9, 9})
	cases := [][]byte{
		good[:len(good)-1], // missing final byte
		{0x00},             // only the clear code started/finished, no EOI
		nil,                // nothing at all
	}
	for _, c := range cases {
		var b bytes.Buffer
		d := NewDecoder(&b)
		if _, err := d.Write(c); err != nil {
			t.Fatalf("Write % x: unexpected %v", c, err)
		}
		err := d.Close()
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("Close % x: err=%v want ErrTruncated", c, err)
		}
	}
}

// Write after encoder Close is rejected and does not touch output/state.
func TestEncoderWriteAfterClose(t *testing.T) {
	var b bytes.Buffer
	e := NewEncoder(&b)
	if _, err := e.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), b.Bytes()...)
	n, err := e.Write([]byte("x"))
	if n != 0 || !errors.Is(err, ErrWriteAfterClose) {
		t.Fatalf("Write after Close=%d,%v", n, err)
	}
	if !bytes.Equal(b.Bytes(), before) {
		t.Fatal("Write after Close mutated output")
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !bytes.Equal(b.Bytes(), before) {
		t.Fatal("second Close mutated output")
	}
}

// A rejected encoder Write (post-Close) must not change the state: the stream
// produced before the rejection still decodes exactly.
func TestRejectedEncoderCallPreservesStream(t *testing.T) {
	var b bytes.Buffer
	e := NewEncoder(&b)
	e.Close()
	_, _ = e.Write([]byte("abc"))
	out := decodeChunked(t, b.Bytes(), 1)
	if len(out) != 0 {
		t.Fatalf("stream after rejected writes decodes to %d bytes", len(out))
	}
}
