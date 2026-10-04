// Package refund 计算按收货分级的行累计应退。
package refund

import (
	"errors"
	"math/big"
)

var ErrInvalid = errors.New("refund: invalid argument")

func New(betaPercent int64) (*Calculator, error) {
	if betaPercent < 0 || betaPercent > 100 {
		return nil, ErrInvalid
	}
	return &Calculator{beta: betaPercent}, nil
}

type Calculator struct {
	beta int64
}

// Cumulative 返回行累计应退 floor(paid*(100*a+beta*b)/(100*shipped))。
// 使用 math/big 完成等价于 128 位的整数运算（名义中间值可达 1e20）。
func (c *Calculator) Cumulative(paid, shipped, a, b int64) int64 {
	num := new(big.Int)
	num.Mul(big.NewInt(paid), big.NewInt(100*a+c.beta*b))
	den := big.NewInt(100 * shipped)
	num.Quo(num, den)
	return num.Int64()
}
