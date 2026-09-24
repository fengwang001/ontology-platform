package cursor

import (
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		score float64
		id    string
		dir   Direction
	}{
		{"forward", 1.5, "r0001", Forward},
		{"backward", -3.25, "r9999", Backward},
		{"zero-score", 0, "a", Forward},
		{"empty-id", 2.0, "", Backward},
		{"long-id", 7.5, "row-with-a-much-longer-identifier-0123456789", Forward},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Decode(Encode(tc.score, tc.id, tc.dir))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if c.Score != tc.score || c.ID != tc.id || c.Dir != tc.dir || c.Start {
				t.Fatalf("got %+v", c)
			}
		})
	}
}

func TestEmptyCursorIsStart(t *testing.T) {
	for _, b := range [][]byte{nil, {}} {
		c, err := Decode(b)
		if err != nil || !c.Start {
			t.Fatalf("empty cursor: c=%+v err=%v", c, err)
		}
	}
}

func TestBitFlipAllRejected(t *testing.T) {
	valid := Encode(1.5, "r0007", Forward)
	var truncated, direction, checksum, accepted int
	for i := range valid {
		for bit := 0; bit < 8; bit++ {
			mut := make([]byte, len(valid))
			copy(mut, valid)
			mut[i] ^= 1 << uint(bit)
			_, err := Decode(mut)
			switch {
			case err == nil:
				accepted++
				t.Errorf("byte %d bit %d: tampered cursor accepted", i, bit)
			case errors.Is(err, ErrTruncated):
				truncated++
			case errors.Is(err, ErrDirection):
				direction++
			case errors.Is(err, ErrChecksum):
				checksum++
			default:
				t.Errorf("byte %d bit %d: unclassified error %v", i, bit, err)
			}
		}
	}
	total := len(valid) * 8
	if accepted != 0 || truncated+direction+checksum != total {
		t.Fatalf("total=%d accepted=%d truncated=%d direction=%d checksum=%d",
			total, accepted, truncated, direction, checksum)
	}
	if truncated == 0 || direction == 0 || checksum == 0 {
		t.Fatalf("expected all three classes non-empty: %d %d %d",
			truncated, direction, checksum)
	}
	t.Logf("variants=%d truncated=%d direction=%d checksum=%d accepted=0",
		total, truncated, direction, checksum)
}

func TestMalformed(t *testing.T) {
	valid := Encode(1.5, "r0007", Forward)
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"too-short", valid[:5], ErrTruncated},
		{"truncated-id", valid[:len(valid)-3], ErrTruncated},
		{"bad-direction", append([]byte{0x99}, valid[1:]...), ErrDirection},
		{"bad-checksum", append([]byte{}, valid[:len(valid)-1]...), ErrChecksum},
	}
	// bad-checksum case: flip one payload bit inside the ID field.
	badCRC := make([]byte, len(valid))
	copy(badCRC, valid)
	badCRC[12] ^= 0x40
	cases[3].b = badCRC
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.b); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeAllocationsConstant(t *testing.T) {
	ids := []string{"a", "row-0001", "a-very-long-identifier-that-keeps-going-0123456789abcdef"}
	for _, id := range ids {
		b := Encode(1.0, id, Forward)
		a := testing.AllocsPerRun(200, func() { _, _ = Decode(b) })
		if a > 1 {
			t.Fatalf("id len %d: %v allocs, want <= 1 (constant)", len(id), a)
		}
	}
}
