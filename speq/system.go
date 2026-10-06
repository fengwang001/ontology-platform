package speq

import "sync"

// System 特种设备检验周期与超期管控系统。
//
// 所有方法可并发调用；内部以单一读写锁串行化状态变更，
// 可串行化（等价于某个串行顺序）。
type System struct {
	mu sync.RWMutex

	categories map[string]CategoryConfig
	objects    map[string]*obj
	// attachments[deviceID] = 该设备当前挂接附件编号集合。
	attachments map[string]map[string]struct{}
	// expiryIndex 未报废/未封存/未停用对象的 (到期日,编号) 索引。
	expiryIndex *treap

	// lastDate 上一个被接受的带日期操作的日期。
	lastDate int
}

// New 创建空系统。
func New() *System {
	return &System{
		categories:  make(map[string]CategoryConfig),
		objects:     make(map[string]*obj),
		attachments: make(map[string]map[string]struct{}),
		expiryIndex: newTreap(),
		lastDate:    -1,
	}
}
