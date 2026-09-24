package cursor

import (
	"errors"
	"testing"

	"ontology/row"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		c    Cursor
	}{
		{"forward basic", Cursor{Forward, row.Row{Score: 1.5, ID: "r1"}, row.Row{Score: 0, ID: "r0"}}},
		{"backward", Cursor{Backward, row.Row{Score: -2, ID: "z"}, row.Row{Score: -3, ID: "a"}}},
		{"unicode id", Cursor{Forward, row.Row{Score: 1, ID: "行-甲"}, row.Row{Score: 0, ID: "" + "a"}}},
		{"equal score", Cursor{Forward, row.Row{Score: 1, ID: "b"}, row.Row{Score: 1, ID: "a"}}},
		{"large id", Cursor{Backward, row.Row{Score: 1e9, ID: string(make([]byte, 300))}, row.Row{Score: 0, ID: "s"}}},
	}
	// 含空字节的 ID 单独设置（make 出的串即全零字节，再覆盖 unicode 一例）。
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := Encode(tc.c)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			got, err := Decode(b)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Dir != tc.c.Dir || row.Compare(got.Last, tc.c.Last) != 0 ||
				row.Compare(got.First, tc.c.First) != 0 {
				t.Fatalf("round trip mismatch: %+v vs %+v", got, tc.c)
			}
		})
	}
}

func TestEmptyAndTruncation(t *testing.T) {
	c, err := Decode(nil)
	if err != nil || c.Dir != Forward {
		t.Fatalf("empty cursor must be legal start, got %+v %v", c, err)
	}
	base, err := Encode(Cursor{Forward, row.Row{Score: 1, ID: "abc"}, row.Row{Score: 0, ID: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	cuts := []int{0, 1, 2, 10, 20, len(base) - 5, len(base) - 1}
	for _, n := range cuts {
		if _, err := Decode(base[:n]); n == 0 {
			continue
		} else if !errors.Is(err, ErrIncomplete) {
			t.Fatalf("truncation at %d -> %v, want ErrIncomplete", n, err)
		}
	}
}

func TestBitFlips(t *testing.T) {
	base, err := Encode(Cursor{Forward, row.Row{Score: 2.5, ID: "node-9"}, row.Row{Score: 2.5, ID: "node-1"}})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[error]int{}
	for byteIdx := 0; byteIdx < len(base); byteIdx++ {
		for bit := uint(0); bit < 8; bit++ {
			variant := append([]byte(nil), base...)
			variant[byteIdx] ^= 1 << bit
			got, derr := Decode(variant)
			if derr == nil {
				t.Fatalf("bit flip byte=%d bit=%d was ACCEPTED: %+v", byteIdx, bit, got)
			}
			switch {
			case errors.Is(derr, ErrDirection):
				counts[ErrDirection]++
			case errors.Is(derr, ErrChecksum):
				counts[ErrChecksum]++
			case errors.Is(derr, ErrIncomplete):
				counts[ErrIncomplete]++
			default:
				t.Fatalf("unclassified error: %v", derr)
			}
		}
	}
	total := len(base) * 8
	if sum := counts[ErrDirection] + counts[ErrChecksum] + counts[ErrIncomplete]; sum != total {
		t.Fatalf("classified %d of %d variants (%v)", sum, total, counts)
	}
	t.Logf("bitflip distribution over %d bits: checksum=%d incomplete=%d direction=%d",
		total, counts[ErrChecksum], counts[ErrIncomplete], counts[ErrDirection])
	if counts[ErrDirection] == 0 || counts[ErrChecksum] == 0 {
		t.Fatalf("expected both checksum and direction classes represented: %v", counts)
	}
}

func TestCrossDirectionAndBadEncode(t *testing.T) {
	fc, _ := Encode(Cursor{Forward, row.Row{Score: 1, ID: "a"}, row.Row{Score: 0, ID: "b"}})
	dec, err := Decode(fc)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(dec, Backward); !errors.Is(err, ErrMismatch) {
		t.Fatalf("forward cursor backward = %v, want ErrMismatch", err)
	}
	if err := EnsureDir(dec, Forward); err != nil {
		t.Fatalf("forward cursor forward: %v", err)
	}
	bad := []struct {
		c   Cursor
		err error
	}{
		{Cursor{Direction(9), row.Row{Score: 1, ID: "a"}, row.Row{Score: 0, ID: "b"}}, ErrDirection},
		{Cursor{Forward, row.Row{}, row.Row{Score: 0, ID: "b"}}, ErrIncomplete},
	}
	for i, tc := range bad {
		if _, err := Encode(tc.c); !errors.Is(err, tc.err) {
			t.Fatalf("bad encode #%d: %v want %v", i, err, tc.err)
		}
	}
}

func TestDecodeAllocsConstant(t *testing.T) {
	short, _ := Encode(Cursor{Forward, row.Row{Score: 1, ID: "a"}, row.Row{Score: 0, ID: "b"}})
	long, _ := Encode(Cursor{Forward, row.Row{Score: 1, ID: string(make([]byte, 400))}, row.Row{Score: 0, ID: "b"}})
	fn := func(b []byte) func() {
		return func() {
			if _, err := Decode(b); err != nil {
				t.Fatal(err)
			}
		}
	}
	as := testing.AllocsPerRun(100, fn(short))
	al := testing.AllocsPerRun(100, fn(long))
	if as > 4 || al > 4 {
		t.Fatalf("decode allocs not bounded constant: short=%v long=%v", as, al)
	}
	t.Logf("decode allocs: short=%v long=%v", as, al)
}
