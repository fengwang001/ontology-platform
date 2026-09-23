package change

import (
	"math"
	"testing"
)

func TestCodecRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		c    Change
	}{
		{"insert", Change{Ver: 1, Op: Insert, ID: "r1", Group: "g", Value: 3.5}},
		{"delete", Change{Ver: 2, Op: Delete, ID: "r2", Group: "g2", Value: -1}},
		{"update-move", Change{Ver: 3, Op: Update, ID: "r3", Group: "b", Value: 2,
			OldGroup: "a", OldValue: 1}},
		{"empty-group", Change{Ver: 4, Op: Insert, ID: "r4", Group: "", Value: 0}},
		{"neg-zero", Change{Ver: 5, Op: Insert, ID: "r5", Group: "g", Value: math.Copysign(0, -1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := EncodeFrame(tc.c)
			got, n, err := DecodeFrameAt(f, 0)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if n != len(f) || !got.EqualKey(tc.c) || got.Ver != tc.c.Ver {
				t.Fatalf("roundtrip mismatch: %+v vs %+v", got, tc.c)
			}
		})
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		c    Change
		want error
	}{
		{"ok", Change{Ver: 1, Op: Insert, ID: "a", Value: 1}, nil},
		{"missing-id", Change{Ver: 1, Op: Insert, ID: "", Value: 1}, ErrMissingID},
		{"nan", Change{Ver: 1, Op: Insert, ID: "a", Value: math.NaN()}, ErrNaN},
		{"old-nan", Change{Ver: 1, Op: Update, ID: "a", Value: 1, OldValue: math.NaN()}, ErrNaN},
		{"bad-op", Change{Ver: 1, Op: Op(9), ID: "a", Value: 1}, ErrBadOp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Valid(); got != tc.want {
				t.Fatalf("Valid = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFrameBoundaries(t *testing.T) {
	f := EncodeFrame(Change{Ver: 7, Op: Insert, ID: "x", Group: "g", Value: 1})
	if _, _, err := DecodeFrameAt(f[:11], 0); err != ErrTruncatedHeader {
		t.Fatalf("11 bytes: %v", err)
	}
	if _, _, err := DecodeFrameAt(f[:15], 0); err != ErrTruncatedLen {
		t.Fatalf("15 bytes: %v", err)
	}
	if _, _, err := DecodeFrameAt(f[:len(f)-1], 0); err != ErrTruncatedBody {
		t.Fatalf("minus 1: %v", err)
	}
	bad := append([]byte(nil), f...)
	bad[len(bad)-1] ^= 0xFF
	if _, _, err := DecodeFrameAt(bad, 0); err != ErrCRC {
		t.Fatalf("corrupt: %v", err)
	}
}
