package stackvm

import "math/bits"

// Fixed per-instruction fees.
const (
	FeePush    uint64 = 1
	FeeAdd     uint64 = 2
	FeeSLoad   uint64 = 5
	FeeSStore  uint64 = 10
	FeeMExpand uint64 = 1
	FeeCall    uint64 = 7
	FeeReturn  uint64 = 1
	FeeFail    uint64 = 3
)

// gasForwardDivisor is the all-but-one-64th forwarding divisor.
const gasForwardDivisor uint64 = 64

// memoryCost is the cumulative cost of having expanded memory to n words:
//
//	cost(n) = 3*n + n*n/512
//
// Cost is monotonic in n; each expansion only charges the difference
// cost(newHigh) - cost(oldHigh). The result saturates at the maximum uint64
// instead of overflowing.
func memoryCost(n uint64) uint64 {
	linear := saturatingMul(n, 3)
	// n*n/512 as a 128-bit product shifted right by 9; saturates on overflow.
	hi, lo := bits.Mul64(n, n)
	var quad uint64
	if hi >= 1<<9 {
		quad = ^uint64(0)
	} else {
		quad = hi<<55 | lo>>9
	}
	return saturatingAdd(linear, quad)
}

// forwardGas implements the call forwarding rule. After the CALL fee is paid
// and r remains, the child receives min(requested, r - r/64).
func forwardGas(r, requested uint64) uint64 {
	allowance := r - r/gasForwardDivisor
	if requested < allowance {
		return requested
	}
	return allowance
}

func saturatingAdd(a, b uint64) uint64 {
	if a > ^uint64(0)-b {
		return ^uint64(0)
	}
	return a + b
}

func saturatingMul(a, b uint64) uint64 {
	if b != 0 && a > ^uint64(0)/b {
		return ^uint64(0)
	}
	return a * b
}
