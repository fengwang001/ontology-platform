package bits

import "testing"

// LastChecks 是测试专用白盒垫片：读取非导出的检查位计数。
// 它只存在于 _test.go（仅测试编译期可见），公开 API 与 demo 都无法读到该值。
func LastChecks(r *Reader) int { return r.lastChecks }

// GammaBits 是朴素参照：手工写出 n 的 gamma 位串（k 个 0 + (k+1) 位二进制）。
func GammaBits(n int64) string {
	k := uint(0)
	for v := n; v > 1; v >>= 1 {
		k++
	}
	b := make([]byte, 0, 2*k+1)
	for i := uint(0); i < k; i++ {
		b = append(b, '0')
	}
	for i := int(k); i >= 0; i-- {
		b = append(b, '0'+byte((n>>uint(i))&1))
	}
	return string(b)
}

// NaivePack 是朴素参照：手工按大端序把位串打包，末字节低位补 0。
func NaivePack(s string) []byte {
	o := []byte{}
	var c byte
	n := uint(0)
	for i := range s {
		c, n = c<<1+(s[i]-'0'), n+1
		if n == 8 {
			o, c, n = append(o, c), 0, 0
		}
	}
	if n > 0 {
		o = append(o, c<<(8-n))
	}
	return o
}

// NaiveStreamEncode 是朴素参照：逐元素拼 gamma 位串再大端打包（不导入 gcode）。
func NaiveStreamEncode(vs []int64) []byte {
	var s string
	for _, n := range vs {
		s += GammaBits(n)
	}
	return NaivePack(s)
}

// NaiveDecode 是朴素参照：逐位手解。kind：0=正常（含全 0 填充），1=截断，2=溢出。
func NaiveDecode(p []byte) (vals []int64, kind int) {
	o := []int64{}
	i, end := 0, len(p)*8
	for i < end {
		k := 0
		for i < end && (p[i/8]>>(7-uint(i%8)))&1 == 0 {
			k, i = k+1, i+1
		}
		if i >= end {
			return o, 0
		}
		i++
		if k > 62 {
			return nil, 2
		}
		v := int64(1) << uint(k)
		for j := k - 1; j >= 0; j-- {
			if i >= end {
				return nil, 1
			}
			v |= int64((p[i/8]>>(7-uint(i%8)))&1) << uint(j)
			i++
		}
		o = append(o, v)
	}
	return o, 0
}

// TestPrefixFree 核验朴素参照 gamma 码两两无前缀；gcode 输出与该参照逐字节相同
// （见外部包 TestReference），故 gcode 码无前缀由本测试与该等价性共同钉住。
func TestPrefixFree(t *testing.T) {
	cs := map[string]bool{}
	for n := 1; n <= 256; n++ {
		cs[GammaBits(int64(n))] = true
	}
	for c := range cs {
		for d := range cs {
			if c != d && len(d) >= len(c) && d[:len(c)] == c {
				t.Fatalf("code %q is prefix of %q", c, d)
			}
		}
	}
}

func TestBitsIO(t *testing.T) {
	cases := []struct {
		s    string
		want []byte
	}{
		{"", []byte{}},
		{"1", []byte{0x80}},
		{"1010011001010001000", []byte{0xA6, 0x51, 0x00}},
	}
	for _, c := range cases {
		w := NewWriter()
		for _, b := range c.s {
			w.WriteBit(int(b - '0'))
		}
		if got := w.Bytes(); string(got) != string(c.want) {
			t.Fatalf("pack %q -> %x, want %x", c.s, got, c.want)
		}
		r := NewReader(c.want)
		for _, b := range c.s {
			x, ok := r.ReadBit()
			if !ok || x != int(b-'0') {
				t.Fatalf("readback %q", c.s)
			}
		}
		for { // 有效位之后，剩余的只能是末字节低位补的 0
			x, ok := r.ReadBit()
			if !ok {
				break
			}
			if x != 0 {
				t.Fatalf("nonzero padding after %q", c.s)
			}
		}
	}
	sr := NewStreamReader()
	sr.Feed([]byte{0xA6})
	sr.Feed([]byte{0x51, 0x00})
	r2 := NewReader([]byte{0xA6, 0x51, 0x00})
	for {
		a, oa := sr.ReadBit()
		b, ob := r2.ReadBit()
		if oa != ob || a != b {
			t.Fatal("stream feed differs from one-shot reader")
		}
		if !oa {
			break
		}
	}
}
