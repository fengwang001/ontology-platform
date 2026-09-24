package codec

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"sync"
	"testing"

	"ontology/vint"
)

var boundaryInts = []int64{math.MinInt64, -2, -1, 0, 1, 2, 63, 64, math.MaxInt64}

func TestRoundtrip(t *testing.T) {
	for _, v := range boundaryInts {
		enc := EncodeInt(v)
		got, n, err := DecodeInt(enc)
		if err != nil || got != v || n != len(enc) {
			t.Fatalf("v=%d got=%d n=%d err=%v", v, got, n, err)
		}
	}
	if err := SelfCheck(boundaryInts); err != nil {
		t.Fatal(err)
	}
}

func TestLengthAndRead(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 16383, 16384, 1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28, 1<<63 - 1, 1 << 63, math.MaxUint64}
	var buf [16]byte
	for _, v := range cases {
		n := vint.PutUvarint(buf[:], v)
		got, read, err := vint.Uvarint(buf[:n])
		if n > 10 || err != nil || got != v || read != n {
			t.Fatalf("v=%d n=%d got=%d read=%d err=%v", v, n, got, read, err)
		}
	}
}

func TestSliceRoundtrip(t *testing.T) {
	got, err := DecodeSlice(EncodeSlice(boundaryInts))
	if err != nil || !slices.Equal(got, boundaryInts) {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestSplit(t *testing.T) {
	enc := EncodeSlice(boundaryInts)
	for cut := 0; cut <= len(enc); cut++ {
		got, err := DecodeSlice(enc[:cut])
		ok := errors.Is(err, vint.ErrIncomplete) ||
			err == nil && slices.Equal(got, boundaryInts[:len(got)])
		if !ok {
			t.Fatalf("cut=%d got=%v err=%v", cut, got, err)
		}
	}
}

func TestErrors(t *testing.T) {
	bufs := [][]byte{{0x80}, {0x80, 0x00}, bytes.Repeat([]byte{0x80}, 10),
		append(bytes.Repeat([]byte{0xff}, 9), 0x02)}
	wants := []error{vint.ErrIncomplete, vint.ErrNonMinimal, vint.ErrOverflow, vint.ErrOverflow}
	for i := range bufs {
		if _, _, err := DecodeInt(bufs[i]); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: got %v want %v", i, err, wants[i])
		}
	}
	if err := SelfCheck(boundaryInts); err != nil { // still usable after rejects
		t.Fatal(err)
	}
}

func TestNoMutation(t *testing.T) {
	buf, orig := []byte{0x01, 0x80, 0x00}, []byte{0x01, 0x80, 0x00}
	got, err := DecodeSlice(buf)
	if err == nil || got != nil || !bytes.Equal(buf, orig) {
		t.Fatalf("got=%v err=%v mutated=%v", got, err, !bytes.Equal(buf, orig))
	}
}

func TestConcurrent(t *testing.T) {
	want := EncodeSlice(boundaryInts)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := DecodeSlice(EncodeSlice(boundaryInts))
			ok := err == nil && slices.Equal(got, boundaryInts) && bytes.Equal(EncodeSlice(boundaryInts), want)
			if !ok {
				t.Error("mismatch")
			}
		}()
	}
	wg.Wait()
}
