package sparse

import "math"

// kahanSum 是 Kahan 补偿求和器。
//
// 为什么需要它：朴素按序累加在量级悬殊时会丢精度
// （例如 1e16 + 1 在 float64 里直接等于 1e16）。补偿求和记录
// 被舍入丢掉的低位，使结果尽量接近真实和。
//
// 与“求和顺序无关”的承诺：点积各项始终按下标升序产生（输入
// 要求严格升序），而打乱后必须重新按下标排序才能输入，因此进入
// 求和器的项序列逐位相同，配合 Kahan 的确定性整数运算式更新，
// 最终 Float64bits 必然逐位一致。
type kahanSum struct {
	sum float64
	c   float64
}

func (k *kahanSum) add(x float64) {
	y := x - k.c
	t := k.sum + y
	k.c = (t - k.sum) - y
	k.sum = t
}

func (k *kahanSum) value() float64 { return k.sum }

// finite 判断值是否为有限数（排除 NaN 与正负无穷）。
func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
