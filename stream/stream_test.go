package stream

import (
	"bytes"
	stdbase64 "encoding/base64"
	"errors"
	"fmt"
	"testing"
)

func decodeSplit(t *testing.T, input []byte, mime bool, n int) ([]byte, error, int64) {
	t.Helper()
	d := NewDecoder(mime, -1)
	for i := 0; i < len(input); i += n {
		end := min(i+n, len(input))
		if _, err := d.Write(input[i:end]); err != nil {
			return d.Output(), err, d.CheckedBytes()
		}
	}
	return d.Output(), d.Close(), d.CheckedBytes()
}

func TestSemantics(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		mime   bool
		want   string
		err    error
		offset int64
	}{
		{"empty", "", false, "", nil, 0},
		{"one", "QQ==", false, "A", nil, 0},
		{"bad one bits", "QR==", false, "", ErrNonCanonicalTail, 3},
		{"two", "QUI=", false, "AB", nil, 0},
		{"bad two bits", "QUJ=", false, "", ErrNonCanonicalTail, 3},
		{"missing both", "QQ", false, "", ErrLength, 1},
		{"missing one", "QQ=", false, "", ErrLength, 2},
		{"extra padding", "QQ===", false, "A", ErrPaddingPosition, 4},
		{"late padding", "QQ==QQ==", false, "A", ErrPaddingPosition, 4},
		{"invalid char", "Q!==", false, "", ErrInvalidCharacter, 1},
		{"newline disabled", "QQ\r\n==", false, "", ErrLineBreak, 2},
		{"newline inside", "Q\r\nQ==", true, "", ErrLineBreak, 1},
		{"bare cr", "QQ==\r", true, "A", ErrLineBreak, 4},
		{"valid mime", "QUJD\r\nREVG", true, "ABCDEF", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode([]byte(tc.in), tc.mime, -1)
			if string(got) != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("got %q,%v want %q,%v", got, err, tc.want, tc.err)
			}
			var offsetErr *OffsetError
			if tc.err != nil && (!errors.As(err, &offsetErr) || offsetErr.Offset != tc.offset) {
				t.Fatalf("offset=%v want=%d", err, tc.offset)
			}
		})
	}
}

func TestSplitPoints(t *testing.T) {
	inputs := [][]byte{
		[]byte("QQ=="),
		[]byte("QQ==QUI="),
		[]byte("QUJD\r\nREVG\r\nSElI"),
		[]byte("Q\rQ=="),
		[]byte("QQ=!=="),
	}
	for _, input := range inputs {
		want, wantErr, _ := decodeSplit(t, input, true, len(input)+1)
		for size := 1; size <= len(input)+1; size++ {
			got, err, checked := decodeSplit(t, input, true, size)
			if !bytes.Equal(got, want) || !sameErrorClass(err, wantErr) || errorOffset(err) != errorOffset(wantErr) {
				t.Fatalf("input %q size %d: %q,%v want %q,%v", input, size, got, err, want, wantErr)
			}
			wantChecked := int64(len(input))
			if wantErr != nil {
				wantChecked = errorOffset(wantErr) + 1
			}
			if checked != wantChecked {
				t.Fatalf("checked %d want %d", checked, wantChecked)
			}
		}
	}
}

func sameErrorClass(a, b error) bool {
	classes := []error{ErrInvalidCharacter, ErrNonCanonicalTail, ErrPaddingPosition, ErrLength, ErrLineBreak}
	for _, class := range classes {
		if errors.Is(a, class) || errors.Is(b, class) {
			return errors.Is(a, class) && errors.Is(b, class)
		}
	}
	return a == nil && b == nil
}

func errorOffset(err error) int64 {
	var offsetErr *OffsetError
	if errors.As(err, &offsetErr) {
		return offsetErr.Offset
	}
	return -1
}

func TestRoundTrip(t *testing.T) {
	samples := []string{"", "A", "AB", "ABC"}
	for _, sample := range samples {
		for _, mime := range []bool{false, true} {
			encoded := Encode([]byte(sample), mime)
			decoded, err := Decode(encoded, mime, -1)
			if err != nil || string(decoded) != sample {
				t.Fatalf("decode %q: %q,%v", encoded, decoded, err)
			}
			standard := stdbase64.StdEncoding.EncodeToString([]byte(sample))
			if !mime && string(encoded) != standard {
				t.Fatalf("encode %q got %q want %q", sample, encoded, standard)
			}
		}
	}
}

func TestMIMEEncoding(t *testing.T) {
	raw := bytes.Repeat([]byte("x"), 60)
	got := string(Encode(raw, true))
	want := stdbase64.StdEncoding.EncodeToString(raw)
	want = want[:76] + "\r\n" + want[76:]
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	back, err := Decode([]byte(got), true, -1)
	if err != nil || !bytes.Equal(back, raw) {
		t.Fatalf("decode: %q,%v", back, err)
	}
}

func TestLimit(t *testing.T) {
	d := NewDecoder(false, 3)
	_, err := d.Write([]byte("QUJDQUI="))
	if !errors.Is(err, ErrOutputLimit) || string(d.Output()) != "ABC" {
		t.Fatalf("got %q,%v", d.Output(), err)
	}
	if _, err := d.Write([]byte("QQ==")); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("after limit: %v", err)
	}
	if !errors.Is(d.Close(), ErrOutputLimit) {
		t.Fatalf("close after limit: %v", d.Close())
	}
}

func TestCheckedCounter(t *testing.T) {
	input := append(bytes.Repeat([]byte("QUJD"), 349525-1), []byte("QQ==")...)
	if len(input) != 1398104 {
		t.Fatalf("input length %d", len(input))
	}
	d := NewDecoder(false, -1)
	for _, c := range input {
		if _, err := d.Write([]byte{c}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil || d.CheckedBytes() != int64(len(input)) {
		t.Fatalf("err=%v checked=%d want=%d", err, d.CheckedBytes(), len(input))
	}
	if len(d.Output()) != 1024*1024 {
		t.Fatalf("output length %d", len(d.Output()))
	}
	if fmt.Sprint(ErrInvalidCharacter) == "" {
		t.Fatal("unreachable")
	}
}
