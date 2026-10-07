package microgrid

// forecastBook 保存关键负荷预测与本地发电盈余。
// 没有预测值的时隙视为不可规划；盈余缺省为零。
type forecastBook struct {
	forecast map[int]int
	surplus  map[int]int
	queries  int // 备用判定访问次数，用于性能可验证性
}

func newForecastBook() *forecastBook {
	return &forecastBook{
		forecast: make(map[int]int),
		surplus:  make(map[int]int),
	}
}

func (b *forecastBook) setForecast(slot, value int) {
	b.forecast[slot] = value
}

func (b *forecastBook) forecastAt(slot int) (int, bool) {
	v, ok := b.forecast[slot]
	return v, ok
}

// forecastOr 返回该时隙关键负荷预测，缺失时按 def 计。
func (b *forecastBook) forecastOr(slot, def int) int {
	if v, ok := b.forecast[slot]; ok {
		return v
	}
	return def
}

func (b *forecastBook) setSurplus(slot, value int) {
	b.surplus[slot] = value
}

func (b *forecastBook) surplusAt(slot int) int {
	return b.surplus[slot]
}

// forget 清除已执行时隙的预测与盈余，使开销不随历史增长。
func (b *forecastBook) forget(slot int) {
	delete(b.forecast, slot)
	delete(b.surplus, slot)
}

// reserveNeed 返回 slot 之后连续 horizon 个时隙的关键负荷预测之和（缺失按 0 计）。
// 开销为 O(horizon)，与预测总长度无关。
func (b *forecastBook) reserveNeed(slot, horizon int) int {
	sum := 0
	for i := 1; i <= horizon; i++ {
		b.queries++
		if v, ok := b.forecast[slot+i]; ok {
			sum += v
		}
	}
	return sum
}
