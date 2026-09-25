// Package conv 把 lex 切好的四段按 I.F(R) 公式精确转成既约分数（全程整数运算、逐步检出溢出）。
package conv

import (
	"errors"
	"math"
	"sync/atomic"

	"ontology/lex"
)

// ErrOverflow 分子或分母在任一累加/乘幂/交叉求和步超出 int64。
var ErrOverflow = errors.New("conv: int64 overflow")

// mulBy10 记录转换过程中「乘以 10」（位值左移）的总次数。
// 非导出内部仪表，不出现在公开接口，不构成可观测状态。
var mulBy10 atomic.Int64

// Convert 把 p 转成既约分数的分子、分母（d>0，符号在分子，零归一为 0/1）。
func Convert(p lex.Parts) (int64, int64, error) {
	var muls int64
	// 整数部分与小数部分合成一段数字单趟累加：numIF = I·10^j + F。
	numIF, err := accum(p.Int+p.Frac, &muls)
	if err == nil {
		var den int64
		den, err = pow10(len(p.Frac)) // 快速幂，不做「乘以 10」
		if err == nil {
			num, d := numIF, den
			if p.Rep != "" {
				num, d, err = withRep(numIF, den, p.Rep, &muls)
			}
			if err == nil {
				if p.Neg {
					num = -num // num ≤ MaxInt64，取负不会溢出
				}
				num, d = reduce(num, d)
				mulBy10.Add(muls)
				return num, d, nil
			}
		}
	}
	mulBy10.Add(muls) // 失败路径同样提交已扫描的计数（内部仪表）
	return 0, 0, err
}

// withRep 处理循环节：n = numIF·(10^k−1) + R；d = 10^j·(10^k−1)。
func withRep(numIF, den int64, rep string, muls *int64) (int64, int64, error) {
	numR, err := accum(rep, muls)
	if err != nil {
		return 0, 0, err
	}
	pk, err := pow10(len(rep))
	if err != nil {
		return 0, 0, err
	}
	t, err := checkedMul(numIF, pk-1)
	if err != nil {
		return 0, 0, err
	}
	num, err := checkedAdd(t, numR)
	if err != nil {
		return 0, 0, err
	}
	d, err := checkedMul(den, pk-1)
	if err != nil {
		return 0, 0, err
	}
	return num, d, nil
}

// accum 单趟扫描数字串：每位一次位值左移（×10，计入计数器）并累加，
// 每步检出溢出；检出后继续扫描计数（保证单趟线性可测），不再累加。
// 首数字直接初始化，故「乘以 10」次数为 len(s)−1。
func accum(s string, muls *int64) (int64, error) {
	num := int64(s[0] - '0')
	var retErr error
	for i := 1; i < len(s); i++ {
		*muls++
		if retErr != nil {
			continue
		}
		t, err := checkedMul(num, 10)
		if err != nil {
			retErr = err
			continue
		}
		num, retErr = checkedAdd(t, int64(s[i]-'0'))
	}
	return num, retErr
}

// pow10 快速幂算 10^k（k≥0），底数自乘不属「乘以 10」，每步检出溢出。
func pow10(k int) (int64, error) {
	result, base := int64(1), int64(10)
	for k > 0 {
		if k&1 == 1 {
			r, err := checkedMul(result, base)
			if err != nil {
				return 0, err
			}
			result = r
		}
		k >>= 1
		if k > 0 {
			b, err := checkedMul(base, base)
			if err != nil {
				return 0, err
			}
			base = b
		}
	}
	return result, nil
}

func checkedMul(a, b int64) (int64, error) {
	if a != 0 && b != 0 && (a > math.MaxInt64/b || a < math.MinInt64/b) {
		return 0, ErrOverflow
	}
	return a * b, nil
}

func checkedAdd(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// reduce 约分并规范化：d>0，gcd(|n|,d)==1，零归一为 0/1。
func reduce(n, d int64) (int64, int64) {
	if n == 0 {
		return 0, 1
	}
	g := gcd(abs64(n), d)
	return n / g, d / g
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
