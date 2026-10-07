package cpe

import (
	"fmt"
	"testing"
)

// 大规模扫掠：40 个种子 × 1500 步随机序列的差分对照。
func TestDifferentialSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("sweep disabled in short mode")
	}
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("sweep-%d", seed), func(t *testing.T) {
			runRandomSeq(t, seed*7919+13, 1500)
		})
	}
}
