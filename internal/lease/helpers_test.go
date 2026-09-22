package lease

// testClock 是测试专用的可手动推进逻辑时钟。
// 实现内部不调用 time.Now()，所有过期行为都由它确定性地复现。
type testClock struct{ now int64 }

func (c *testClock) Now() int64      { return c.now }
func (c *testClock) Set(t int64)     { c.now = t }
func (c *testClock) Advance(d int64) { c.now += d }

// newTestManager 返回一个 Manager 与其时钟，初始时刻为 0。
func newTestManager() (*Manager, *testClock) {
	c := &testClock{}
	return New(c.Now), c
}
