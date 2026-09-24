// Package alpha 提供 base32 字符表：5 位值与字符的双向映射及非法字符判定。
package alpha

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

const invalid = 0xFF

var rev = func() (t [256]byte) {
	for i := range t {
		t[i] = invalid
	}
	for i := 0; i < len(alphabet); i++ {
		t[alphabet[i]] = byte(i)
	}
	return
}()

// Char 返回 5 位值 v（取低 5 位）对应的字符。
func Char(v byte) byte { return alphabet[v&31] }

// Valid 报告 c 是否在字符表内。
func Valid(c byte) bool { return rev[c] != invalid }

// Value 返回字符 c 对应的 5 位值；c 非法时行为未定义，应先用 Valid 判定。
func Value(c byte) byte { return rev[c] & 31 }
