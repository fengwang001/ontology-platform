package ftl

import "fmt"

// Config 闪存几何与策略参数。
type Config struct {
	NumBlocks     int // 擦除块总数
	PagesPerBlock int // 每块页数
	LogicalPages  int // 逻辑页号范围 [0, LogicalPages)

	LowWatermark  int // 空闲块低水位，必须 >= 2
	HighWatermark int // 空闲块高水位，Low < High < NumBlocks

	EraseLimit    int // 块寿命上限（擦除次数），达到即退役
	WearThreshold int // 静态磨损均衡阈值（擦除次数差）
}

func (c Config) validate() error {
	if c.NumBlocks < 1 {
		return fmt.Errorf("ftl: NumBlocks must be >= 1, got %d", c.NumBlocks)
	}
	if c.PagesPerBlock < 1 {
		return fmt.Errorf("ftl: PagesPerBlock must be >= 1, got %d", c.PagesPerBlock)
	}
	if c.LogicalPages < 1 {
		return fmt.Errorf("ftl: LogicalPages must be >= 1, got %d", c.LogicalPages)
	}
	if c.LowWatermark < 2 {
		return fmt.Errorf("ftl: LowWatermark must be >= 2, got %d", c.LowWatermark)
	}
	if c.HighWatermark <= c.LowWatermark {
		return fmt.Errorf("ftl: HighWatermark (%d) must be > LowWatermark (%d)", c.HighWatermark, c.LowWatermark)
	}
	if c.HighWatermark >= c.NumBlocks {
		return fmt.Errorf("ftl: HighWatermark (%d) must be < NumBlocks (%d)", c.HighWatermark, c.NumBlocks)
	}
	if c.EraseLimit < 1 {
		return fmt.Errorf("ftl: EraseLimit must be >= 1, got %d", c.EraseLimit)
	}
	if c.WearThreshold < 0 {
		return fmt.Errorf("ftl: WearThreshold must be >= 0, got %d", c.WearThreshold)
	}
	return nil
}
