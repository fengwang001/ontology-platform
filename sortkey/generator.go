package sortkey

import "fmt"

// Generator 是可插入排序键生成器的接口，可替换实现。
type Generator interface {
	// Between 生成严格落在 left 与 right 之间的新键。
	// 空串表示该侧无邻居（负无穷 / 正无穷）。
	// 同一对邻居反复调用必须得到同一个结果。
	Between(left, right string) (string, error)
	// EvenKeys 返回 n 个严格递增、等间距分布的短键，供重排使用。
	EvenKeys(n int) ([]string, error)
	// Validate 校验键只包含字符集内的字符。
	Validate(key string) error
	// Limit 返回键长度上限。
	Limit() int
}

// Fractional 是基于分数索引的 Generator 默认实现。
type Fractional struct {
	maxLen int
}

// NewFractional 创建一个键长度上限为 maxLen 的生成器。
func NewFractional(maxLen int) *Fractional {
	if maxLen < 1 {
		maxLen = 1
	}
	return &Fractional{maxLen: maxLen}
}

// Limit 实现 Generator。
func (f *Fractional) Limit() int { return f.maxLen }

// Validate 实现 Generator。除字符集外，键还必须满足规范形式：
// 不得以字符集最小字符 '0' 结尾（否则它与去掉末尾 '0' 的前缀
// 同值，左侧会形成无法再细分的死路）。生成器自身产出的键
// 始终满足规范形式。
func (f *Fractional) Validate(key string) error {
	for i := 0; i < len(key); i++ {
		if _, ok := digitOf(key[i]); !ok {
			return &InvalidKeyError{Key: key, Pos: i, Char: key[i]}
		}
	}
	if len(key) > 0 && key[len(key)-1] == Alphabet[0] {
		return &InvalidKeyError{Key: key, Pos: len(key) - 1, Char: Alphabet[0]}
	}
	return nil
}

// Between 实现 Generator。生成结果确定：只依赖 left 与 right。
func (f *Fractional) Between(left, right string) (string, error) {
	if err := f.Validate(left); err != nil {
		return "", err
	}
	if err := f.Validate(right); err != nil {
		return "", err
	}
	if right != "" && left >= right {
		return "", ErrInvalidOrder
	}
	key := midpoint(left, right)
	if len(key) > f.maxLen {
		return "", ErrNeedsRebalance
	}
	return key, nil
}

// midpoint 返回严格落在 left 与 right 之间的键。
// 调用前必须保证 left < right（空串表示无界）。
// 递归深度以输入键长为界，必然终止。
func midpoint(left, right string) string {
	n := max(len(left), len(right))
	for i := 0; i <= n; i++ {
		l := digitAt(left, i, 0)
		h := digitAt(right, i, base)
		if l == h {
			continue
		}
		prefix := prefixOf(left, i)
		if h-l >= 2 {
			// 左右数字之间还有空位，取中点即可。
			return prefix + string(Alphabet[(l+h)/2])
		}
		// 左右数字相邻：沿用左键当前位，在更低位继续细分。
		return prefix + string(Alphabet[l]) + midpoint(tailOf(left, i+1), "")
	}
	return "" // left < right 时不可达
}

// EvenKeys 实现 Generator：在定宽 width 的规范键（末位非 '0'）
// 中等间距地取 n 个。宽度为 width 的规范键共 36^(width-1)*35 个，
// 按数值序第 j 个对应整数 j + j/35 + 1。
func (f *Fractional) EvenKeys(n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	width, count := 1, base-1
	for count < n {
		width++
		count *= base
	}
	if width > f.maxLen {
		return nil, fmt.Errorf("sortkey: %d elements need key width %d, exceeds limit %d", n, width, f.maxLen)
	}
	keys := make([]string, n)
	for i := range keys {
		j := (i + 1) * count / (n + 1)
		keys[i] = encode(j+j/(base-1)+1, width)
	}
	return keys, nil
}

// encode 把非负整数 v 编码为定宽 width 的键（左侧补 '0'）。
func encode(v, width int) string {
	buf := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		buf[i] = Alphabet[v%base]
		v /= base
	}
	return string(buf)
}
