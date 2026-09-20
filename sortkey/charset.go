// Package sortkey provides a pluggable sort-key generator.
//
// 一组元素靠字符串排序键决定先后，键的字典序（Go 的字符串 < 比较）
// 就是元素的顺序。生成器支持在任意两个相邻元素之间插入新元素，
// 而不改动其他元素的键。
//
// # 字符集
//
// 所有键只能由以下固定且有序的字符集构成（共 36 个字符）：
//
//	0123456789abcdefghijklmnopqrstuvwxyz
//
// 该字符集的顺序与字节序一致，因此键的字典序可以直接用字符串
// 比较得到。键必须满足规范形式：不得以字符集最小字符 '0' 结尾，
// 否则它与去掉末尾 '0' 的前缀同值，左侧会形成无法再细分的死路。
// 满足规范形式的任意两个键之间都可以无限细分
// （参见 Fractional.Between）。
//
// # 生成规则
//
// 把键看作以字符集为进制的分数（类似小数 0.xxx），Between 返回
// 严格落在左右邻居之间的中点键。规则完全确定：同一对邻居反复
// 调用必然得到同一个键。三种边界均支持：
//   - 最前面插入：左邻居传 ""，表示负无穷；
//   - 最后面插入：右邻居传 ""，表示正无穷；
//   - 两键之间插入：传入左右邻居的键。
//
// # 长度上限
//
// 反复在同一位置插入会让键不断变长。生成器声明一个长度上限，
// 当新键长度将超过上限时返回 ErrNeedsRebalance，而不是生成
// 超长键或静默截断。此时应调用 Sequence.Rebalance 重新分配
// 等间距的短键。
package sortkey

// Alphabet 是键允许使用的固定有序字符集。
const Alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// base 是字符集大小，即键的进制。
const base = len(Alphabet)

// digitOf 返回字符在字符集中的序号；不属于字符集时 ok 为 false。
func digitOf(c byte) (digit int, ok bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10, true
	}
	return 0, false
}

// digitAt 返回键 s 第 i 位的数字；越界时返回默认值 def。
// 左邻居越界位视为 0（最小），右邻居越界位视为 base（最大之外）。
func digitAt(s string, i, def int) int {
	if i < len(s) {
		if d, ok := digitOf(s[i]); ok {
			return d
		}
	}
	return def
}

// prefixOf 返回键 s 的前 i 位；当 i 超出 s 的长度时，用字符集
// 最小字符 '0' 补齐（等价于分数末尾隐含的 0）。
func prefixOf(s string, i int) string {
	if i <= len(s) {
		return s[:i]
	}
	pad := make([]byte, i-len(s))
	for j := range pad {
		pad[j] = Alphabet[0]
	}
	return s + string(pad)
}

// tailOf 返回键 s 第 i 位之后的部分；越界时返回空串。
func tailOf(s string, i int) string {
	if i >= len(s) {
		return ""
	}
	return s[i:]
}
