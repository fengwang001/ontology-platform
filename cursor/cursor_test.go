package cursor

import (
	"errors"
	"testing"

	"ontology/row"
)

func TestRoundTrip(t *testing.T) {
	cases := []Cursor{
		{Dir: Forward, Key: 0, ID: "a"},
		{Dir: Backward, Key: -1.5, ID: "row-0042"},
		{Dir: Forward, Key: 3.14e300, ID: ""},
		{Dir: Backward, Key: -0.0, ID: "x"},
	}
	for _, want := range cases {
		got, err := Decode(Encode(want))
		if err != nil {
			t.Fatalf("Decode(%+v): %v", want, err)
		}
		if got != want {
			t.Fatalf("round trip: got %+v, want %+v", got, want)
		}
	}
}

func TestEmptyCursor(t *testing.T) {
	c, err := Decode(nil)
	if err != nil || !c.Empty() {
		t.Fatalf("empty decode: c=%+v err=%v", c, err)
	}
	if got := Encode(c); got != nil {
		t.Fatalf("empty encode: got %v, want nil", got)
	}
	for _, dir := range []Direction{Forward, Backward} {
		if err := c.RequireDir(dir); err != nil {
			t.Fatalf("empty cursor must allow any direction: %v", err)
		}
	}
}

// 对合法游标逐字节逐比特翻转，每个变体都必须被拒绝并正确分类。
func TestBitFlipTamper(t *testing.T) {
	raw := Encode(After(row.Row{Key: 2.5, ID: "row-0042"}, Forward))
	counts := map[error]int{ErrDirection: 0, ErrTruncated: 0, ErrChecksum: 0}
	for i := range raw {
		for bit := 0; bit < 8; bit++ {
			bad := append([]byte(nil), raw...)
			bad[i] ^= 1 << bit
			if _, err := Decode(bad); err == nil {
				t.Fatalf("byte %d bit %d: tampered cursor accepted", i, bit)
			} else {
				matched := false
				for target := range counts {
					if errors.Is(err, target) {
						counts[target]++
						matched = true
					}
				}
				if !matched {
					t.Fatalf("byte %d bit %d: unclassified error %v", i, bit, err)
				}
			}
		}
	}
	total := 8 * len(raw)
	want := map[error]int{ErrDirection: 8, ErrTruncated: 8, ErrChecksum: total - 16}
	for target, n := range want {
		if counts[target] != n {
			t.Fatalf("%v: got %d variants, want %d", target, counts[target], n)
		}
	}
}

// 任意真前缀都因字段不完整被拒。
func TestTruncated(t *testing.T) {
	raw := Encode(After(row.Row{Key: 1, ID: "abc"}, Backward))
	for n := 1; n < len(raw); n++ {
		if _, err := Decode(raw[:n]); !errors.Is(err, ErrTruncated) {
			t.Fatalf("prefix %d: got %v, want ErrTruncated", n, err)
		}
	}
}

func TestCrossDirection(t *testing.T) {
	c := After(row.Row{Key: 1, ID: "a"}, Forward)
	if err := c.RequireDir(Backward); !errors.Is(err, ErrCrossDir) {
		t.Fatalf("got %v, want ErrCrossDir", err)
	}
	if err := c.RequireDir(Forward); err != nil {
		t.Fatalf("same direction: %v", err)
	}
	for _, target := range []error{ErrChecksum, ErrTruncated, ErrDirection} {
		if errors.Is(ErrCrossDir, target) {
			t.Fatalf("ErrCrossDir must be distinct from %v", target)
		}
	}
}

// 解码分配次数为常数，与数据集规模无关。
func TestDecodeAllocs(t *testing.T) {
	raw := Encode(After(row.Row{Key: 9, ID: "row-9999"}, Forward))
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := Decode(raw); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 3 {
		t.Fatalf("decode allocs = %v, want <= 3 (constant)", allocs)
	}
}
