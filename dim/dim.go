// Package dim 定义 CUBE 的 cell 键：每维是「具体值」或「ALL」，
// ALL 用独立标志位表示，绝不与空串混同（"" 是合法具体值）。
package dim

// Key 是一个 cell 的键。Conc[i] 为 true 表示第 i 维取具体值 V[i]，
// 为 false 表示该维 ALL（此时 V[i] 无意义，恒为 ""）。
type Key struct {
	V    [3]string
	Conc [3]bool
}

// All 返回全通配键 (*,*,*)，level 0。
func All() Key { return Key{} }

// Concrete 返回三维全具体的键，level 3。
func Concrete(a, b, c string) Key {
	return Key{V: [3]string{a, b, c}, Conc: [3]bool{true, true, true}}
}

// Level 返回键中具体（非 ALL）维度的个数，取值 0..3。
func Level(k Key) int {
	n := 0
	for _, c := range k.Conc {
		if c {
			n++
		}
	}
	return n
}

// Cells 枚举一个事实恰好归属的 8 个 cell 键：
// 掩码 m 的第 i 位为 1 表示第 i 维取具体值，为 0 表示 ALL。
func Cells(a, b, c string) [8]Key {
	v := [3]string{a, b, c}
	var out [8]Key
	for m := 0; m < 8; m++ {
		var k Key
		for i := 0; i < 3; i++ {
			if m&(1<<i) != 0 {
				k.Conc[i] = true
				k.V[i] = v[i]
			}
		}
		out[m] = k
	}
	return out
}
