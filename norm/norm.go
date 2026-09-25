// Package norm 实现限定字母表上的 Unicode 规范归一化（NFD/NFC）与规范等价键。
package norm

import "errors"
import "unicode/utf8"

var ErrInvalidUTF8 = errors.New("norm: invalid utf-8")
var ErrUnsupported = errors.New("norm: unsupported code point")

const (
	sBase, sMax = 0xAC00, 0xD7A3
	lBase, lMax = 0x1100, 0x1112
	vBase, vMax = 0x1161, 0x1175
	tBase, tMax = 0x11A7, 0x11C2
)

// cccTab 组合类表；字母表内未列出的码点均为 0（起始符）。
var cccTab = map[rune]int{0x0327: 202, 0x0323: 220,
	0x0300: 230, 0x0301: 230, 0x0308: 230, 0x030A: 230}

func ccc(r rune) int { return cccTab[r] }

// decomp 规范分解表，键即九个预组合字符；U+212B 递归分解为 A+ring。
var decomp = map[rune][]rune{
	0x00E8: {0x65, 0x0300}, 0x00E9: {0x65, 0x0301},
	0x00C7: {0x43, 0x0327}, 0x00E7: {0x63, 0x0327},
	0x00C4: {0x41, 0x0308}, 0x00C5: {0x41, 0x030A},
	0x00E4: {0x61, 0x0308}, 0x1EB9: {0x65, 0x0323},
	0x212B: {0x00C5},
}

type pair struct{ s, m rune }

// compose 主组合表（decomp 的逆）。U+212B 组合排除：不在表中，永不生成。
var compose = map[pair]rune{
	{0x65, 0x0300}: 0x00E8, {0x65, 0x0301}: 0x00E9,
	{0x43, 0x0327}: 0x00C7, {0x63, 0x0327}: 0x00E7,
	{0x41, 0x0308}: 0x00C4, {0x41, 0x030A}: 0x00C5,
	{0x61, 0x0308}: 0x00E4, {0x65, 0x0323}: 0x1EB9,
}

func supported(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
		r == 0x0300 || r == 0x0301 || r == 0x0308 || r == 0x030A || r == 0x0323 || r == 0x0327,
		r >= lBase && r <= lMax, r >= vBase && r <= vMax,
		r >= tBase+1 && r <= tMax, r >= sBase && r <= sMax:
		return true
	}
	_, ok := decomp[r]
	return ok
}

func decomposeRune(r rune, out *[]rune) {
	if sBase <= r && r <= sMax { // 韩文音节算法分解
		s := int(r) - sBase
		*out = append(*out, rune(lBase+s/588), rune(vBase+(s%588)/28))
		if t := s % 28; t > 0 {
			*out = append(*out, rune(tBase+t))
		}
		return
	}
	if d, ok := decomp[r]; ok { // 递归分解直至稳定
		for _, x := range d {
			decomposeRune(x, out)
		}
		return
	}
	*out = append(*out, r)
}

// order 规范排序：起始符不动，每段组合标记按 ccc 稳定插入排序。
func order(d []rune) {
	for i := 1; i < len(d); i++ {
		for j := i; ccc(d[j]) > 0 && j > 0 && ccc(d[j-1]) > ccc(d[j]); j-- {
			d[j-1], d[j] = d[j], d[j-1]
		}
	}
}

// NFD 完全规范分解 + 规范排序。
func NFD(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrInvalidUTF8
	}
	var d []rune
	for _, r := range s {
		if !supported(r) {
			return "", ErrUnsupported
		}
		decomposeRune(r, &d)
	}
	order(d)
	return string(d), nil
}

func isLV(r rune) bool { return sBase <= r && r <= sMax && (int(r)-sBase)%28 == 0 }

// composeRunes 对 NFD 结果做规范组合。prev 是段内最近保留标记的 ccc
// （段已升序，即最大阻断值）；组合成功的标记被消费，不计入。
func composeRunes(d []rune) []rune {
	if len(d) == 0 {
		return d
	}
	out := append(make([]rune, 0, len(d)), d[0])
	si, prev := 0, ccc(d[0])
	for _, r := range d[1:] {
		c := ccc(r)
		if c == 0 {
			s, adj := out[si], si == len(out)-1
			switch {
			case adj && lBase <= s && s <= lMax && vBase <= r && r <= vMax:
				out[si] = rune(sBase + ((int(s)-lBase)*21+int(r)-vBase)*28) // L+V
			case adj && isLV(s) && tBase+1 <= r && r <= tMax:
				out[si] = s + r - tBase // LV+T
			default:
				out = append(out, r)
				si, prev = len(out)-1, 0
			}
			continue
		}
		if prev < c { // 未被阻断才尝试主组合
			if cp, ok := compose[pair{out[si], r}]; ok {
				out[si] = cp
				continue
			}
		}
		out = append(out, r)
		prev = c
	}
	return out
}

// NFC 先 NFD 再规范组合。
func NFC(s string) (string, error) {
	nfd, err := NFD(s)
	if err != nil {
		return "", err
	}
	return string(composeRunes([]rune(nfd))), nil
}

// Key 规范等价键：NFC(s)。规范等价的字符串键相同。
func Key(s string) (string, error) { return NFC(s) }
