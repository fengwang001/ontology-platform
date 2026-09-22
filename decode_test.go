package ontology

import (
	"errors"
	"testing"
)

func TestDecodeTruncated(t *testing.T) {
	desc := []bool{false, true, false}
	row := []any{int64(-123456), "ab\x00c", 2.5}
	full := NewEncoder(desc).Encode(row)
	for cut := 0; cut < len(full); cut++ {
		_, err := Decode(desc, full[:cut])
		if err == nil {
			t.Fatalf("cut=%d: expected error", cut)
		}
		var de *DecodeError
		if !errors.As(err, &de) {
			t.Fatalf("cut=%d: not a DecodeError: %v", cut, err)
		}
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("cut=%d: got %v, want ErrTruncated", cut, err)
		}
		if de.KeyIndex < 0 || de.KeyIndex > 2 {
			t.Fatalf("cut=%d: bad key index %d", cut, de.KeyIndex)
		}
	}
	// A cut at a key boundary must name the missing key.
	one := NewEncoder(desc).Encode(row[:1])
	_, err := Decode(desc, one)
	var de *DecodeError
	if !errors.As(err, &de) || de.KeyIndex != 1 || !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated at key 1, got %v", err)
	}
}

func TestDecodeTrailing(t *testing.T) {
	desc := []bool{false, false}
	full := NewEncoder(desc).Encode([]any{int64(1), "x"})
	_, err := Decode(desc, append(append([]byte{}, full...), 0x00))
	var de *DecodeError
	if !errors.As(err, &de) || !errors.Is(err, ErrTrailing) || de.KeyIndex != 2 {
		t.Fatalf("want ErrTrailing at key 2, got %v", err)
	}
}

func TestDecodeBadTag(t *testing.T) {
	good := NewEncoder(nil).Encode([]any{"ok"})
	bad := append(append([]byte{}, good...), 0x7F)
	_, err := Decode([]bool{false, false}, bad)
	var de *DecodeError
	if !errors.As(err, &de) || !errors.Is(err, ErrBadTag) || de.KeyIndex != 1 {
		t.Fatalf("want ErrBadTag at key 1, got %v", err)
	}
	// Bad numeric type byte after the zero tag.
	_, err = Decode(nil, []byte{tagZero, 0x09})
	if !errors.Is(err, ErrBadTag) {
		t.Fatalf("want ErrBadTag, got %v", err)
	}
	// Bad tag inside a descending key (after un-flipping).
	_, err = Decode([]bool{true}, []byte{^byte(0x7F)})
	if !errors.Is(err, ErrBadTag) {
		t.Fatalf("want ErrBadTag for flipped key, got %v", err)
	}
}

func TestDecodeNilDescReadsToEnd(t *testing.T) {
	row := []any{int64(1), "a", nil}
	back, err := Decode(nil, NewEncoder(nil).Encode(row))
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 3 || back[0] != int64(1) || back[1] != "a" || back[2] != nil {
		t.Fatalf("got %v", back)
	}
	if _, err := Decode(nil, []byte{}); err != nil {
		t.Fatalf("empty input should decode to zero keys: %v", err)
	}
}
