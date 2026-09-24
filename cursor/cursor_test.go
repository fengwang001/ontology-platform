package cursor

import (
	"errors"
	"testing"

	"ontology/row"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		k    row.Row
		d    Direction
	}{
		{"forward normal", row.New(1.5, "id-1"), Forward},
		{"backward zero", row.New(0, ""), Backward},
		{"backward negative", row.New(-3.25, "a/b"), Backward},
		{"unicode id", row.New(2, "行-①"), Forward},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := Encode(tc.k, tc.d)
			got, atStart, err := Decode(raw, tc.d)
			if err != nil || atStart {
				t.Fatalf("Decode = %v,%v,%v", got, atStart, err)
			}
			if got.Dir != tc.d || got.Key != tc.k {
				t.Fatalf("round trip mismatch: %+v vs %+v", got, tc.k)
			}
		})
	}
}

func TestEmptyCursor(t *testing.T) {
	cases := []Direction{Forward, Backward}
	for _, d := range cases {
		if _, atStart, err := Decode(nil, d); !atStart || err != nil {
			t.Fatalf("nil cursor dir=%d: atStart/err = %v,%v", d, atStart, err)
		}
		if _, atStart, err := Decode([]byte{}, d); !atStart || err != nil {
			t.Fatalf("empty cursor dir=%d: atStart/err = %v,%v", d, atStart, err)
		}
	}
}

func TestCrossDirection(t *testing.T) {
	cases := []struct {
		enc, dec Direction
	}{
		{Forward, Backward},
		{Backward, Forward},
	}
	for _, tc := range cases {
		raw := Encode(row.New(1, "x"), tc.enc)
		_, _, err := Decode(raw, tc.dec)
		if !errors.Is(err, ErrWrongDirection) {
			t.Fatalf("enc=%d dec=%d err=%v, want ErrWrongDirection", tc.enc, tc.dec, err)
		}
	}
}

func TestMalformedLengthAndVersion(t *testing.T) {
	cases := [][]byte{
		nil, // 空游标合法，单独跳过
		{0},
		{version, dirForward, 0, 0},
		make([]byte, minLen-1),
	}
	for i, data := range cases {
		if i == 0 {
			continue
		}
		if _, _, err := Decode(data, Forward); !errors.Is(err, ErrCursorMalformed) {
			t.Fatalf("case %d err=%v, want ErrCursorMalformed", i, err)
		}
	}
}

// TestBitFlips 对合法游标逐比特翻转，任何变体都不得被接受，
// 并按错误类别统计，供 FINDINGS.md 记录。
func TestBitFlips(t *testing.T) {
	counts := map[error]int{}
	cases := []Direction{Forward, Backward}
	for _, d := range cases {
		raw := Encode(row.New(1.25, "id-ab"), d)
		for byteIdx := 0; byteIdx < len(raw); byteIdx++ {
			for bit := uint(0); bit < 8; bit++ {
				variant := append([]byte(nil), raw...)
				variant[byteIdx] ^= 1 << bit
				got, atStart, err := Decode(variant, d)
				if err == nil {
					t.Fatalf("bit flip dir=%d byte=%d bit=%d 被误接受: %+v", d, byteIdx, bit, got)
				}
				if atStart {
					t.Fatalf("bit flip dir=%d byte=%d bit=%d 被当成从头开始", d, byteIdx, bit)
				}
				switch {
				case errors.Is(err, ErrChecksum):
					counts[ErrChecksum]++
				case errors.Is(err, ErrCursorMalformed):
					counts[ErrCursorMalformed]++
				case errors.Is(err, ErrBadDirection):
					counts[ErrBadDirection]++
				default:
					t.Fatalf("无法分类的错误: %v", err)
				}
			}
		}
	}
	t.Logf("bit-flip 分类统计: checksum=%d malformed=%d badDirection=%d",
		counts[ErrChecksum], counts[ErrCursorMalformed], counts[ErrBadDirection])
}

// 解码是 O(1)：固定游标格式下分配次数为常数（仅 ID 拷贝一处）。
func TestDecodeAllocsConstant(t *testing.T) {
	raw := Encode(row.New(1.25, "id-ab"), Forward)
	if allocs := testing.AllocsPerRun(100, func() {
		_, _, _ = Decode(raw, Forward)
	}); allocs != 1 {
		t.Fatalf("Decode allocs/run = %.2f, want 1", allocs)
	}
}
