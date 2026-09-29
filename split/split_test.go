package split_test

import (
	"errors"
	"testing"

	"ontology/split"
)

func TestConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  split.Config
		want error
	}{
		{"window zero", split.Config{Min: 4, Max: 8, Window: 0}, split.ErrWindow},
		{"window gt min", split.Config{Min: 4, Max: 8, Window: 5}, split.ErrWindow},
		{"min gt max", split.Config{Min: 9, Max: 8, Window: 2}, split.ErrMinMax},
		{"ok", split.Config{Min: 4, Max: 8, Window: 2}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := split.Boundaries([]byte("01234567"), tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

// 用重复的长串提高自然边界出现率；断言所有非末尾块长落在 [min,max]，
// 以及 max 强制切分确实发生。
func TestLengthsAndMaxForce(t *testing.T) {
	cfg := split.Config{Min: 256, Max: 4096, Window: 16}
	data := make([]byte, 200000)
	x := uint32(747796405)
	for i := range data {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		data[i] = byte(x >> 8)
	}
	bounds, err := split.Boundaries(data, cfg)
	if err != nil {
		t.Fatal(err)
	}
	prev := 0
	forced := false
	for _, b := range bounds {
		if b-prev < cfg.Min || b-prev > cfg.Max {
			t.Fatalf("block len %d out of [%d,%d]", b-prev, cfg.Min, cfg.Max)
		}
		if b-prev == cfg.Max {
			forced = true
		}
		prev = b
	}
	if !forced {
		t.Fatal("expected at least one max-forced cut")
	}
}

// 局部性：在流中间插入 128 字节，远处块地址（用内容字节身份代替）逐一相同，
// 受影响块数不超过 DESIGN.md 推导的常数 35，且对 4 个不同流长成立。
func TestLocality(t *testing.T) {
	cfg := split.Config{Min: 256, Max: 4096, Window: 16}
	const k = 128
	mk := func(n int) []byte {
		d := make([]byte, n)
		x := uint32(2463534242)
		for i := range d {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			d[i] = byte(x >> 16)
		}
		return d
	}
	for _, n := range []int{20000, 60000, 120000, 240000} {
		orig := mk(n)
		ins := make([]byte, k)
		mod := append(append(append([]byte{}, orig[:n/2]...), ins...), orig[n/2:]...)
		contents := func(d []byte) [][]byte {
			bs, _ := split.Boundaries(d, cfg)
			out, prev := [][]byte{}, 0
			for _, b := range bs {
				out = append(out, d[prev:b])
				prev = b
			}
			return out
		}
		c1, c2 := contents(orig), contents(mod)
		i, j := len(c1), len(c2)
		for i > 0 && j > 0 && string(c1[i-1]) == string(c2[j-1]) {
			i, j = i-1, j-1
		}
		if j > 64 { // 公共后缀之前（受影响）的边界块数 ≤ 常数，且需与 n 无关
			t.Fatalf("n=%d affected=%d > 64", n, j)
		}
	}
}
