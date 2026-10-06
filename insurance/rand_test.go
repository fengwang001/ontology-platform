package insurance

import "math/rand"

// randSource 返回确定性随机源，便于测试失败时复现。
func randSource(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}
