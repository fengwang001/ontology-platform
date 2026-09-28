package weightedsample

import (
	"log/slog"
	"sync"
)

// Element 描述一个带权输入元素。
type Element struct {
	ID     string
	Weight float64
}

// Item 是进入样本的元素及其计算结果。
type Item struct {
	Element
	Key   float64
	Order int
}

// Sampler 是并发安全的加权不放回流式抽样器。
type Sampler struct {
	mu     sync.Mutex
	k      int
	source RandomSource
	logger *slog.Logger
}

// New 创建容量为 k 的抽样器；随机源与日志器由调用方注入。
func New(k int, source RandomSource, logger *slog.Logger) (*Sampler, error) {
	return nil, nil
}

// Submit 提交一个带权元素；任何拒绝都不改变抽样器与随机源状态。
func (s *Sampler) Submit(id string, weight float64) error {
	return nil
}

// Samples 返回当前样本的快照，按键值降序、到达先后升序排列。
func (s *Sampler) Samples() []Item {
	return nil
}

// Consumed 返回已消耗的随机数个数。
func (s *Sampler) Consumed() int {
	return 0
}

// Check 执行内部不变量自检。
func (s *Sampler) Check() error {
	return nil
}
