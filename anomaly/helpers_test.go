package anomaly

import "math/rand"

// rngSeed 给所有随机测试固定种子，保证测试可复现。
func rngSeed() rand.Source {
	return rand.NewSource(20260930)
}
