package naivemodel

import "math/big"

// ratio 为朴素模型自用的有理数薄封装。
type ratio struct{ n, d *big.Int }

func newRat() *ratio { return &ratio{n: big.NewInt(0), d: big.NewInt(1)} }

func (r *ratio) set(x *big.Rat) *ratio {
	r.n = new(big.Int).Set(x.Num())
	r.d = new(big.Int).Set(x.Denom())
	return r
}

func (r *ratio) setInt(x int64) *ratio {
	r.n = big.NewInt(x)
	r.d = big.NewInt(1)
	return r
}

func (r *ratio) setZero() *ratio { r.n.SetInt64(0); r.d.SetInt64(1); return r }
func (r *ratio) sign() int       { return r.n.Sign() }

func (r *ratio) clone() *ratio { return &ratio{n: new(big.Int).Set(r.n), d: new(big.Int).Set(r.d)} }

func (r *ratio) norm() {
	if r.d.Sign() < 0 {
		r.n.Neg(r.n)
		r.d.Neg(r.d)
	}
	g := new(big.Int).GCD(nil, nil, new(big.Int).Set(r.n), new(big.Int).Set(r.d))
	r.n.Quo(r.n, g)
	r.d.Quo(r.d, g)
}

func (r *ratio) add(x *ratio) *ratio {
	r.n.Mul(r.n, x.d).Add(r.n, new(big.Int).Mul(x.n, r.d))
	r.d.Mul(r.d, x.d)
	r.norm()
	return r
}

func (r *ratio) sub(x *ratio) *ratio {
	r.n.Mul(r.n, x.d).Sub(r.n, new(big.Int).Mul(x.n, r.d))
	r.d.Mul(r.d, x.d)
	r.norm()
	return r
}

func (r *ratio) addInt(x int64) *ratio { return r.add(&ratio{n: big.NewInt(x), d: big.NewInt(1)}) }
func (r *ratio) subInt(x int64) *ratio { return r.sub(&ratio{n: big.NewInt(x), d: big.NewInt(1)}) }
func (r *ratio) mulInt(x int64) *ratio {
	r.n.Mul(r.n, big.NewInt(x))
	r.norm()
	return r
}

// divInt 返回 r/x。
func (r *ratio) divInt(x int64) *ratio {
	r.d.Mul(r.d, big.NewInt(x))
	r.norm()
	return r
}

func (r *ratio) cmpRat(x *big.Rat) int {
	l := new(big.Int).Mul(r.n, x.Denom())
	rr := new(big.Int).Mul(x.Num(), r.d)
	return l.Cmp(rr)
}

func (r *ratio) toRat() *big.Rat {
	return new(big.Rat).SetFrac(new(big.Int).Set(r.n), new(big.Int).Set(r.d))
}

func (r *ratio) cmpInt(x int64) int {
	return r.n.Cmp(new(big.Int).Mul(big.NewInt(x), r.d))
}
