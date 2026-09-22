package message

import (
	"bytes"
	"errors"
	"ontology/wire"
	"testing"
)

// The five syntax error categories, each with a distinct sentinel and a
// correct absolute byte offset.
func TestSyntaxErrorsDistinguishable(t *testing.T) {
	p := newParser(t, Options{})
	cases := []struct {
		name    string
		input   []byte
		want    error
		wantOff int
	}{
		{
			name:  "varint overflow",
			input: cat(vfield(1, 1), append([]byte{0x02, 0x01}, bytes.Repeat([]byte{0x80}, 10)...)),
			// offset: after first field (3 bytes) + header (2 bytes)
			want: wire.ErrVarintOverflow, wantOff: 5,
		},
		{
			name:    "unknown wire type",
			input:   []byte{0x01, 0x63, 0x00},
			want:    wire.ErrUnknownType,
			wantOff: 1,
		},
		{
			name:    "length exceeds remaining",
			input:   []byte{0x02, 0x01, 0x05, 0x41},
			want:    wire.ErrLengthOverflow,
			wantOff: 2,
		},
		{
			name:    "zero field number",
			input:   []byte{0x01, 0x00, 0x01, 0x00},
			want:    wire.ErrZeroFieldNumber,
			wantOff: 3,
		},
		{
			name: "nested length mismatch",
			// Field 3 declares a 3-byte nested message whose content
			// ends mid-varint: declared length != consumed bytes.
			input:   []byte{0x03, 0x02, 0x03, 0x01, 0x00, 0x80},
			want:    wire.ErrLengthMismatch,
			wantOff: 5,
		},
	}
	sentinels := []error{
		wire.ErrVarintOverflow, wire.ErrUnknownType, wire.ErrLengthOverflow,
		wire.ErrZeroFieldNumber, wire.ErrLengthMismatch,
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := p.Parse(tc.input)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			// Must match exactly one of the five categories.
			matched := 0
			for _, s := range sentinels {
				if errors.Is(err, s) {
					matched++
				}
			}
			if matched != 1 {
				t.Fatalf("err matches %d categories, want 1", matched)
			}
			var we *wire.Error
			if !errors.As(err, &we) || we.Offset != tc.wantOff {
				t.Fatalf("offset = %+v, want %d", we, tc.wantOff)
			}
			if m != nil {
				t.Fatal("failed parse must return zero-value message")
			}
		})
	}
}

func TestErrorOffsetsAreAbsolute(t *testing.T) {
	p := newParser(t, Options{})
	// Bad field hides inside a known nested message at depth 2.
	inner := []byte{0x01, 0x7f} // unknown wire type at relative offset 1
	mid := mfield(3, inner)
	top := cat(vfield(1, 1), mfield(3, mid))
	_, err := p.Parse(top)
	var we *wire.Error
	if !errors.As(err, &we) {
		t.Fatalf("err = %v", err)
	}
	// top: 3 bytes varint field + 3 bytes message header = 6; mid header
	// 3 bytes; bad byte at 6 + 3 + 1 = 10.
	if we.Offset != 10 || !errors.Is(err, wire.ErrUnknownType) {
		t.Fatalf("got offset %d err %v, want offset 10 ErrUnknownType", we.Offset, err)
	}
}

func TestTruncatedTopLevel(t *testing.T) {
	p := newParser(t, Options{})
	if _, err := p.Parse([]byte{0x01}); !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestEmptyInput(t *testing.T) {
	p := newParser(t, Options{})
	m := mustParse(t, p, nil)
	if got := m.Marshal(); len(got) != 0 {
		t.Fatalf("empty message marshaled to %v", got)
	}
}
