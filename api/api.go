// Package api 是对外入口：包装 gcode 的 Encode/Decode，并提供 SelfCheck 自检。
// 仅依赖 gcode（进而依赖 bits），依赖方向单向。
package api

import (
	"errors"
	"fmt"

	"ontology/bits"
	"ontology/gcode"
)

// 重新导出三类哨兵错误，调用方无需 import gcode 即可 errors.Is。
var (
	ErrNonPositive = gcode.ErrNonPositive
	ErrTruncated   = gcode.ErrTruncated
	ErrOverflow    = gcode.ErrOverflow
)

// Encode 包装 gcode.Encode：正整数流 → 大端序打包字节。
func Encode(vs []int64) ([]byte, error) { return gcode.Encode(vs) }

// Decode 包装 gcode.Decode：字节流 → 正整数序列，失败整体返回 nil。
func Decode(p []byte) ([]int64, error) { return gcode.Decode(p) }

// naiveEncode 是与 gcode 无关的朴素参照：手工拼 gamma 位串再大端打包。
func naiveEncode(vs []int64) ([]byte, error) {
	var s []int
	for _, n := range vs {
		if n < 1 {
			return nil, gcode.ErrNonPositive
		}
		k := 0
		for v := n; v > 1; v >>= 1 {
			k++
		}
		for i := 0; i < k; i++ {
			s = append(s, 0)
		}
		for b := k; b >= 0; b-- {
			s = append(s, int((n>>uint(b))&1))
		}
	}
	w := bits.NewWriter()
	for _, b := range s {
		w.WriteBit(b)
	}
	return w.Bytes(), nil
}

// naiveDecode 是逐位手解的朴素参照。
func naiveDecode(p []byte) ([]int64, error) {
	r := bits.NewReader(p)
	out := make([]int64, 0)
	for {
		k := 0
		b, ok := r.ReadBit()
		for ok && b == 0 {
			k++
			b, ok = r.ReadBit()
		}
		if !ok {
			return out, nil
		}
		if k > 62 {
			return nil, gcode.ErrOverflow
		}
		n := int64(1) << uint(k)
		for i := k - 1; i >= 0; i-- {
			x, ok := r.ReadBit()
			if !ok {
				return nil, gcode.ErrTruncated
			}
			n |= int64(x) << uint(i)
		}
		out = append(out, n)
	}
}

// SelfCheck 对内置序列核验四条不变量，全部成立返回 nil，否则返回描述性错误。
func SelfCheck() error {
	seqs := [][]int64{{}, {1}, {2}, {1, 2, 3, 5, 8}, {1 << 62, 2, 3, 1 << 40}}
	// 不变量 1：与朴素参照逐字节/逐元素一致。
	for _, v := range seqs {
		got, err := gcode.Encode(v)
		want, err2 := naiveEncode(v)
		if !errors.Is(err, err2) || string(got) != string(want) {
			return fmt.Errorf("reference encode mismatch for %v", v)
		}
		d1, e1 := gcode.Decode(got)
		d2, e2 := naiveDecode(want)
		if !errors.Is(e1, e2) || fmt.Sprint(d1) != fmt.Sprint(d2) {
			return fmt.Errorf("reference decode mismatch for %v", v)
		}
	}
	// 不变量 2：切分点无关（遍历所有字节切点）。
	b, _ := gcode.Encode(seqs[3])
	want, _ := gcode.Decode(b)
	for cut := 0; cut <= len(b); cut++ {
		sd := gcode.NewStreamDecoder()
		o1, err := sd.Feed(b[:cut])
		o2, err2 := sd.Feed(b[cut:])
		if err != nil || err2 != nil || sd.End() != nil {
			return fmt.Errorf("streaming failed at cut %d", cut)
		}
		if fmt.Sprint(append(o1, o2...)) != fmt.Sprint(want) {
			return fmt.Errorf("chunk-independence mismatch at cut %d", cut)
		}
	}
	// 不变量 3：确定性 + 无前缀（取一组码两两检查前缀关系）。
	codes := map[string]bool{}
	for n := int64(1); n <= 256; n++ {
		cb, _ := gcode.Encode([]int64{n})
		codes[string(cb)] = true
	}
	for c := range codes {
		for d := range codes {
			if c != d && len(d) >= len(c) && d[:len(c)] == c {
				return fmt.Errorf("prefix-free violated: %s prefix of %s", c, d)
			}
		}
	}
	b1, _ := gcode.Encode(seqs[3])
	b2, _ := gcode.Encode(seqs[3])
	if string(b1) != string(b2) {
		return errors.New("determinism violated")
	}
	// 不变量 4：失败整体返回 nil；Decode 为纯函数（每次新建 Reader），失败后再用好输入仍成功。
	if d, err := gcode.Decode([]byte{0x01}); err == nil || d != nil {
		return errors.New("truncated decode leaked partial output")
	}
	if d, err := gcode.Decode([]byte{0, 0, 0, 0, 0, 0, 0, 0x01}); err == nil || d != nil {
		return errors.New("overflow decode leaked partial output")
	}
	if _, err := gcode.Decode(b1); err != nil {
		return errors.New("decoder not usable after failure")
	}
	return nil
}
