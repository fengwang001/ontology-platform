package demand

import "math/big"

// frac 为精确有理数，避免浮点在"恰好等于合同需量"边界上误判。
type frac struct{ v big.Rat }

func newFrac() *frac                { return &frac{} }
func fracInt(n int64) *frac         { f := newFrac(); f.v.SetInt64(n); return f }
func fracOver(num, den int64) *frac { f := newFrac(); f.v.SetFrac64(num, den); return f }

var zero = newFrac()

func (f *frac) set(src *frac) *frac  { f.v.Set(&src.v); return f }
func (f *frac) clone() *frac         { return newFrac().set(f) }
func (f *frac) add(x *frac) *frac    { f.v.Add(&f.v, &x.v); return f }
func (f *frac) sub(x *frac) *frac    { f.v.Sub(&f.v, &x.v); return f }
func (f *frac) mulInt(n int64) *frac { f.v.Mul(&f.v, big.NewRat(n, 1)); return f }
func (f *frac) mulRat(x *frac) *frac { f.v.Mul(&f.v, &x.v); return f }
func (f *frac) neg() *frac           { f.v.Neg(&f.v); return f }
func (f *frac) cmp(x *frac) int      { return f.v.Cmp(&x.v) }
func (f *frac) isZero() bool         { return f.v.Sign() == 0 }

func (f *frac) num() int64 { return f.v.Num().Int64() }
func (f *frac) den() int64 { return f.v.Denom().Int64() }

// ceilFrac 返回精确有理数向上取整后的整数。
func ceilFrac(f *frac) int64 {
	num := f.v.Num()
	den := f.v.Denom()
	q := new(big.Int).Quo(num, den)
	r := new(big.Int).Rem(num, den)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64()
}

// float64 仅用于日志/展示，不参与判定。
func (f *frac) float64() float64 { v, _ := f.v.Float64(); return v }

// Float64 导出精确有理数的浮点近似，仅用于展示与测试断言。
func (f *frac) Float64() float64 { return f.float64() }

// Rat 返回底层精确有理数的拷贝，供测试做精确比对。
func (f *frac) Rat() *big.Rat { return new(big.Rat).Set(&f.v) }
