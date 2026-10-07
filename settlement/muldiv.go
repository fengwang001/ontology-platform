package settlement

import (
	"math"
	"math/big"
)

// 以下辅助函数用 big.Int 计算乘积再除以万分比，避免 int64 溢出；
// 所有入参均非负，故 Quo 等价于向下取整。结果超出 int64 时饱和到 MaxInt64。

// mulDivFloor 返回 floor(a*b / basisPoints)。
func mulDivFloor(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	p.Quo(p, big.NewInt(basisPoints))
	return satInt64(p)
}

// mulDivCeil 返回 ceil(a*b / basisPoints)。
func mulDivCeil(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	q, r := new(big.Int).QuoRem(p, big.NewInt(basisPoints), new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return satInt64(q)
}

// penaltyAmount 返回 floor(overdueDays * dailyRate * total / basisPoints)。
func penaltyAmount(overdueDays, dailyRate, total int64) int64 {
	if overdueDays <= 0 || dailyRate <= 0 || total <= 0 {
		return 0
	}
	p := new(big.Int).Mul(big.NewInt(overdueDays), big.NewInt(dailyRate))
	p.Mul(p, big.NewInt(total))
	p.Quo(p, big.NewInt(basisPoints))
	return satInt64(p)
}

func satInt64(v *big.Int) int64 {
	if !v.IsInt64() {
		return math.MaxInt64
	}
	return v.Int64()
}
