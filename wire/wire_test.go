package wire

import (
	"bytes"
	"errors"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 300, 1<<32 - 1, 1 << 63, 1<<64 - 1}
	for _, v := range vals {
		buf := AppendVarint(nil, v)
		if len(buf) != VarintLen(v) {
			t.Fatalf("VarintLen(%d)=%d, encoded %d bytes", v, VarintLen(v), len(buf))
		}
		got, n, err := ReadVarint(buf)
		if err != nil || got != v || n != len(buf) {
			t.Fatalf("ReadVarint(%v) = %d, %d, %v; want %d, %d, nil", buf, got, n, err, v, len(buf))
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10 continuation bytes: does not terminate within 10 bytes.
	buf := bytes.Repeat([]byte{0x80}, 10)
	if _, _, err := ReadVarint(buf); !errors.Is(err, ErrVarintOverflow) {
		t.Fatalf("got %v, want ErrVarintOverflow", err)
	}
	// 10th byte contributes more than 1 bit.
	buf = append(bytes.Repeat([]byte{0x80}, 9), 0x02)
	if _, _, err := ReadVarint(buf); !errors.Is(err, ErrVarintOverflow) {
		t.Fatalf("got %v, want ErrVarintOverflow", err)
	}
}

func TestVarintTruncated(t *testing.T) {
	if _, _, err := ReadVarint([]byte{0x80}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
	if _, _, err := ReadVarint(nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestReadFieldVarint(t *testing.T) {
	buf := AppendVarintField(nil, 3, 300)
	f, n, err := ReadField(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf) || f.Number != 3 || f.Type != Varint || f.Varint != 300 {
		t.Fatalf("got %+v n=%d", f, n)
	}
}

func TestReadFieldBytes(t *testing.T) {
	buf := AppendField(nil, 2, Bytes, []byte("hello"))
	f, n, err := ReadField(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(buf) || f.Number != 2 || f.Type != Bytes || string(f.Payload) != "hello" {
		t.Fatalf("got %+v n=%d", f, n)
	}
	if f.HeaderLen != n-len("hello") {
		t.Fatalf("HeaderLen=%d", f.HeaderLen)
	}
}

func TestReadFieldErrors(t *testing.T) {
	cases := []struct {
		name    string
		buf     []byte
		want    error
		wantOff int
	}{
		{"zero number", []byte{0x00, 0x00}, ErrZeroFieldNumber, 0},
		{"unknown type", []byte{0x01, 0x7f}, ErrUnknownType, 1},
		{"missing type", []byte{0x01}, ErrTruncated, 1},
		{"length overflow", []byte{0x02, 0x01, 0x05, 0x41}, ErrLengthOverflow, 2},
		{"payload varint overflow", append([]byte{0x01, 0x00}, bytes.Repeat([]byte{0x80}, 10)...), ErrVarintOverflow, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ReadField(tc.buf)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			var we *Error
			if !errors.As(err, &we) || we.Offset != tc.wantOff {
				t.Fatalf("offset = %+v, want %d", we, tc.wantOff)
			}
		})
	}
}

func TestErrorDistinct(t *testing.T) {
	all := []error{ErrVarintOverflow, ErrTruncated, ErrUnknownType, ErrLengthOverflow, ErrZeroFieldNumber, ErrLengthMismatch}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d are not distinguishable", i, j)
			}
		}
	}
}
