package billing

// 精确整数分摊：账期线性归属的余数去向，以及按面积×在住权重的公摊分摊。
// 全部使用 math/big 有理整数运算，不引入浮点，保证精度与守恒。

import "math/big"

// splitFraction 把总量 q 按 num/den 切给前段，余数计入较晚账期。
// 返回 (前段整数, 后段整数)，二者之和恒等于 q（q>=0, 0<=num<=den）。
func splitFraction(q, num, den int64) (front, back int64) {
	if den <= 0 {
		return 0, q
	}
	front = new(big.Int).Quo(new(big.Int).Mul(big.NewInt(q), big.NewInt(num)), big.NewInt(den)).Int64()
	return front, q - front
}

// prorate 把非负总量 total 按权重 nums[i]/sum(nums) 分摊为整数。
// 规则：先取精确份额 floor(total*nums[i]/S)，再把每单位余数依次分给
// 当前「应得-已分」亏欠最大的户（平局取索引较小者）。
// 结果之和恒等于 total；big.Int 运算保证任何中间乘法不溢出、不丢精度。
// 若全部权重为 0（整账期无任何在住），按等权处理，使空置户仍参与公摊。
func prorate(total int64, nums []int64) []int64 {
	n := len(nums)
	out := make([]int64, n)
	T, S := big.NewInt(total), big.NewInt(0)
	for _, x := range nums {
		S.Add(S, big.NewInt(x))
	}
	if S.Sign() == 0 {
		for i := 0; i < n; i++ {
			nums[i] = 1
			S.Add(S, big.NewInt(1))
		}
	}
	rem := new(big.Int).Set(T)
	for i := 0; i < n; i++ {
		q := new(big.Int).Quo(new(big.Int).Mul(T, big.NewInt(nums[i])), S)
		out[i] = q.Int64()
		rem.Sub(rem, q)
	}
	for rem.Sign() > 0 {
		best, bestGap := -1, new(big.Int)
		for i := 0; i < n; i++ {
			got := new(big.Int).Mul(big.NewInt(out[i]), S)
			want := new(big.Int).Mul(T, big.NewInt(nums[i]))
			gap := new(big.Int).Sub(want, got)
			if best < 0 || gap.Cmp(bestGap) > 0 {
				best, bestGap = i, gap
			}
		}
		out[best]++
		rem.Sub(rem, big.NewInt(1))
	}
	return out
}
