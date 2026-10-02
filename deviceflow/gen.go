package deviceflow

import "crypto/rand"

// defaultGenAlphabet 是不含易混淆字符的用户码字母表。
const defaultGenAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// defaultGen 是默认用户码生成器，返回 8 位大写字母数字串。
func defaultGen() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = defaultGenAlphabet[int(b)%len(defaultGenAlphabet)]
	}
	return string(out)
}

// normalizeCode 规范化用户码：去掉全部连字符、字母转大写。
// 规范化后须为 4 到 16 个大写字母或数字，否则 ok 为 false。
func normalizeCode(s string) (norm string, ok bool) {
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-':
			continue
		case c >= 'a' && c <= 'z':
			buf = append(buf, c-'a'+'A')
		default:
			buf = append(buf, c)
		}
	}
	if len(buf) < 4 || len(buf) > 16 {
		return "", false
	}
	for _, c := range buf {
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return "", false
		}
	}
	return string(buf), true
}
