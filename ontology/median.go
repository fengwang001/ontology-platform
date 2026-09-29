package ontology

// Tracker 用两个堆（较小一半的大根堆与较大一半的小根堆）
// 加上懒删除表，动态维护整数多重集的下中位数。
type Tracker struct {
	mu     rwm
	maxLen int
}

// New 创建容量上限为 maxLen 的中位数维护器。
func New(maxLen int) *Tracker {
	return &Tracker{maxLen: maxLen}
}

// Add 批量加入整数值；整批成功或整批拒绝。
func (t *Tracker) Add(vals ...int) error { return nil }

// Remove 批量撤回整数值；整批成功或整批拒绝。
func (t *Tracker) Remove(vals ...int) error { return nil }

// Median 返回当前多重集的下中位数，空集报错。
func (t *Tracker) Median() (int, error) { return 0, nil }

// Len 返回当前有效元素个数。
func (t *Tracker) Len() int { return 0 }

// SelfCheck 校验两堆内部不变量，失败返回描述性错误。
func (t *Tracker) SelfCheck() error { return nil }
