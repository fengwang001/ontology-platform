// Package entry 定义缓存条目及其代价密度。
package entry

// Entry 是一个缓存条目：键、大小、构造代价、频率计数。
// Freq 与 Epoch 配合实现惰性老化：有效频率 = Freq >> (当前纪元 - Epoch)。
type Entry struct {
	Key   string
	Size  int64 // 字节数，0 合法（不占容量，密度按 1 计）
	Cost  int64 // 构造代价，必须 >= 0
	Freq  int64 // 频率计数（按 Epoch 折算）
	Epoch uint64
}

// EffectiveFreq 返回纪元 cur 下的有效频率（每过一个纪元减半）。
func (e *Entry) EffectiveFreq(cur uint64) int64 {
	d := cur - e.Epoch
	if d >= 63 {
		return 0
	}
	return e.Freq >> d
}

// Density 返回代价密度 = 代价 × 有效频率 / 大小（大小 0 按 1 计）。
func (e *Entry) Density(cur uint64) float64 {
	size := e.Size
	if size < 1 {
		size = 1
	}
	return float64(e.Cost) * float64(e.EffectiveFreq(cur)) / float64(size)
}

// Bump 先把频率折算到纪元 cur，再计数一次访问。
func (e *Entry) Bump(cur uint64) {
	e.Freq = e.EffectiveFreq(cur) + 1
	e.Epoch = cur
}
