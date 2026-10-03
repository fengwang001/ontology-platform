package budget

import "math/big"

func bigRat(a, b int64) *big.Rat { return new(big.Rat).SetFrac(big.NewInt(a), big.NewInt(b)) }

func newRat0() *big.Rat { return new(big.Rat) }

func bigRat1() *big.Rat { return new(big.Rat).SetInt64(1) }

// reduce 返回既约分数，分母恒为正；零为 0/1。
func reduce(num, den int64) Fraction {
	if den == 0 {
		panic("budget: zero denominator")
	}
	if den < 0 {
		num, den = -num, -den
	}
	if num == 0 {
		return Fraction{Num: 0, Den: 1}
	}
	r := new(big.Rat).SetFrac(big.NewInt(num), big.NewInt(den))
	return Fraction{Num: r.Num().Int64(), Den: r.Denom().Int64()}
}
