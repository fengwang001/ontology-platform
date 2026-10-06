package threematch

import "sync"

// store 是系统的集中状态注册表，持有全部订单、发票、供应商冻结标志与
// 单调时钟。所有可变状态只允许在持有 mu 时访问，因此单个 *Service 的
// 并发调用天然可串行化（互斥锁保证线性一致，等价于某个串行顺序）。
type store struct {
	mu        sync.Mutex
	clock     int64 // 上一次被接受操作的时刻
	suppliers map[string]*supplierState
	pos       map[string]*poState
	invoices  map[string]*invoiceState // 发票号全局唯一
	// trace 在关键判定点被调用（持锁状态，钩子内不得回调 Service）。
	// 默认 nil，零开销；测试通过 Service.SetTracer 注入。
	trace func(event string, detail map[string]any)
}

func newStore() *store {
	return &store{
		suppliers: make(map[string]*supplierState),
		pos:       make(map[string]*poState),
		invoices:  make(map[string]*invoiceState),
	}
}

// checkClock 必须在持有 mu 时调用：校验单调时钟，返回应记录的时钟错误。
func (s *store) checkClock(at int64) *Error {
	if at < s.clock {
		return newError(KindClockRewind, "操作时刻 %d 早于上一时刻 %d", at, s.clock)
	}
	return nil
}

// advance 必须在操作被接受后、释放锁之前调用。
func (s *store) advance(at int64) {
	s.clock = at
}
