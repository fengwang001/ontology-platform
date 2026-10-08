package constexpr

import (
	"math/big"
	"math/rand"
)

// 随机表达式生成器：在无类型整数/有理数/布尔上生成深度受限的树，
// 并以独立朴素模型为判据对拍。

type gen struct {
	rng   *rand.Rand
	depth int
}

var arithOps = []Op{OpAdd, OpSub, OpMul, OpDiv, OpRem, OpBitAnd, OpBitOr, OpBitXor}
var cmpOps = []Op{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}

// genExpr 生成表达式；boolMode=true 时生成布尔型表达式。
// small=true 时使用小整数（适合再包一层类型转换）。
func (g *gen) genExpr(boolMode bool) *Expr {
	if g.depth <= 0 || g.rng.Intn(3) == 0 {
		return g.genLeaf(boolMode)
	}
	if boolMode {
		switch g.rng.Intn(3) {
		case 0:
			return Unary(OpLogNot, g.genExpr(true))
		case 1:
			return Binary(OpLogAnd, g.genExpr(true), g.genExpr(true))
		default:
			return Binary(cmpOps[g.rng.Intn(len(cmpOps))], g.genExpr(false), g.genExpr(false))
		}
	}
	g.depth--
	defer func() { g.depth++ }()

	switch g.rng.Intn(10) {
	case 0, 1:
		// 有理数运算（避免取余/位运算落到 rat 上频繁报非法）。
		op := []Op{OpAdd, OpSub, OpMul, OpDiv}[g.rng.Intn(4)]
		left := g.genRatLeaf()
		right := g.genRatLeaf()
		if op == OpDiv {
			right = g.genNonZeroRat()
		}
		return Binary(op, left, right)
	case 2:
		return Unary(OpNeg, g.genExpr(false))
	case 3:
		return Unary(OpBitNot, g.genIntExpr())
	case 4:
		// 移位：保证移位量合法、左操作数为整数种类。
		return Binary([]Op{OpShl, OpShr}[g.rng.Intn(2)],
			g.genIntExpr(), g.genShiftCount())
	case 5:
		return Binary(cmpOps[g.rng.Intn(len(cmpOps))], g.genExpr(false), g.genExpr(false))
	default:
		op := arithOps[g.rng.Intn(len(arithOps))]
		left := g.genIntExpr()
		right := g.genIntExpr()
		if op == OpDiv || op == OpRem {
			right = g.genNonZeroInt()
		}
		return Binary(op, left, right)
	}
}

func (g *gen) genLeaf(boolMode bool) *Expr {
	if boolMode {
		return BoolLit(g.rng.Intn(2) == 0)
	}
	switch g.rng.Intn(3) {
	case 0:
		return g.genIntLeaf()
	case 1:
		return g.genRatLeaf()
	default:
		return BoolLit(g.rng.Intn(2) == 0)
	}
}

func (g *gen) genIntExpr() *Expr {
	g.depth--
	defer func() { g.depth++ }()
	if g.depth <= 0 || g.rng.Intn(2) == 0 {
		return g.genIntLeaf()
	}
	op := arithOps[g.rng.Intn(len(arithOps))]
	left := g.genIntExpr()
	right := g.genIntExpr()
	if op == OpDiv || op == OpRem {
		right = g.genNonZeroInt()
	}
	return Binary(op, left, right)
}

func (g *gen) genIntLeaf() *Expr {
	// 以小整数为主，避免快速撞上 512 位；少量大值测试边界。
	n := int64(0)
	switch g.rng.Intn(10) {
	case 0:
		n = g.rng.Int63() - (1 << 40)
	case 1:
		n = int64(1) << uint(g.rng.Intn(60))
	default:
		n = g.rng.Int63n(2000) - 1000
	}
	return IntLit(big.NewInt(n))
}

func (g *gen) genNonZeroInt() *Expr {
	for {
		e := g.genIntLeaf()
		if e.IntVal.Sign() != 0 {
			return e
		}
	}
}

func (g *gen) genRatLeaf() *Expr {
	num := big.NewInt(g.rng.Int63n(401) - 200)
	den := big.NewInt(int64(g.rng.Intn(20) + 1))
	return RatLit(new(big.Rat).SetFrac(num, den))
}

func (g *gen) genNonZeroRat() *Expr {
	for {
		e := g.genRatLeaf()
		if e.RatVal.Sign() != 0 {
			return e
		}
	}
}

func (g *gen) genShiftCount() *Expr {
	// 生成值为 [0,1000] 内整数的节点：整数种类或整数值有理数种类。
	n := big.NewInt(int64(g.rng.Intn(1001)))
	if g.rng.Intn(2) == 0 {
		return IntLit(n)
	}
	return RatLit(new(big.Rat).SetInt(n))
}
