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

var bounds = []int64{math.MinInt64, -2, -1, 0, 1, 2, 300, math.MaxInt64}

func TestRoundTrip(t *testing.T) {
	for _, v := range bounds {
		got, err := DecodeInt(EncodeInt(v))
		if err != nil || got != v {
			t.Fatalf("roundtrip %d: got %d err %v", v, got, err)
		}
	}
}

func TestEncodedLenAndRead(t *testing.T) {
	cases := [][2]uint64{
		{0, 1}, {127, 1}, {128, 2}, {16383, 2}, {16384, 3}, {1<<21 - 1, 3}, {1 << 21, 4},
		{1<<35 - 1, 5}, {1 << 35, 6}, {1<<49 - 1, 7}, {1 << 49, 8}, {1<<63 - 1, 9}, {1 << 63, 10}, {math.MaxUint64, 10},
	}
	for _, c := range cases {
		u, want := c[0], int(c[1])
		var buf [10]byte
		if n := vint.PutUvarint(buf[:], u); n != want || n > 10 {
			t.Fatalf("len(%d)=%d want %d", u, n, want)
		}
		got, n, err := vint.Uvarint(buf[:])
		if err != nil || got != u || n != want {
			t.Fatalf("decode(%d)=(%d,%d,%v) want n=%d", u, got, n, err, want)
		}
	}
}

func TestErrorKinds(t *testing.T) {
	cases := []struct {
		in   []byte
		want error
	}{
		{[]byte{0x80}, vint.ErrIncomplete}, {[]byte{0x80, 0x00}, vint.ErrNonMinimal},
		{bytes.Repeat([]byte{0x80}, 11), vint.ErrOverflow}, {append(bytes.Repeat([]byte{0xff}, 9), 0x02), vint.ErrOverflow},
	}
	for _, c := range cases {
		if _, err := DecodeInt(c.in); !errors.Is(err, c.want) {
			t.Fatalf("decode %x: got %v want %v", c.in, err, c.want)
		}
	}
	if _, err := DecodeInt(EncodeInt(1)); err != nil {
		t.Fatal("codec broken after rejected inputs")
	}
}

func TestSliceSplit(t *testing.T) {
	enc := EncodeSlice(bounds)
	got, err := DecodeSlice(enc)
	if err != nil || !slices.Equal(got, bounds) {
		t.Fatalf("roundtrip: %v err %v", got, err)
	}
	for i := 0; i < len(enc); i++ {
		part, err := DecodeSlice(enc[:i])
		okPrefix := err == nil && slices.Equal(part, bounds[:len(part)])
		if !okPrefix && !errors.Is(err, vint.ErrIncomplete) {
			t.Fatalf("cut %d: part=%v err=%v", i, part, err)
		}
	}
}

func TestFailureNoTrace(t *testing.T) {
	bad := append(EncodeSlice(bounds), 0x80)
	got, err := DecodeSlice(bad)
	same := bytes.Equal(bad, append(EncodeSlice(bounds), 0x80))
	if err == nil || got != nil || !same {
		t.Fatalf("got=%v err=%v same=%v", got, err, same)
	}
}

func TestConcurrent(t *testing.T) {
	want := EncodeSlice(bounds)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if !bytes.Equal(EncodeSlice(bounds), want) {
					t.Error("encode mismatch")
				}
			}
		}()
	}
	wg.Wait()
}
