package remittance

import "sync"

// Config 是系统级配置。
type Config struct {
	QuoteTTLSeconds int64 // 报价有效期 T
	ReviewSeconds   int64 // 人工审核时限 R
	ReviewThreshold int64 // 目标额达到该值即进入人工审核（>=）
}

// Engine 是整个汇款系统的入口。
//
// 所有方法均可并发调用；内部以单一互斥锁串行化，
// 语义等价于按某个全局顺序串行执行。
type Engine struct {
	mu      sync.Mutex
	cfg     Config
	lastNow int64 // 上一次被接受的、携带 now 的操作的时间

	accounts   map[string]*account
	sanctioned map[string]struct{}
	quotes     map[int64]*quote
	transfers  map[int64]*transfer

	nextQuoteID    int64
	nextTransferID int64
}

// New 创建引擎。
func New(cfg Config) *Engine {
	return &Engine{
		cfg:        cfg,
		accounts:   make(map[string]*account),
		sanctioned: make(map[string]struct{}),
		quotes:     make(map[int64]*quote),
		transfers:  make(map[int64]*transfer),
	}
}

// AddSender 注册汇款人及其限额；重复注册覆盖限额（不影响既有占用）。
func (e *Engine) AddSender(id string, lim Limits) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if a, ok := e.accounts[id]; ok {
		a.lim = lim
		return
	}
	e.accounts[id] = newAccount(id, lim)
}

// AddSanctionedPayee / RemoveSanctionedPayee 维护制裁名单。
// 制裁名单维护不携带时间、不推进全局时钟，因为它表示外部事实而非业务操作。
func (e *Engine) AddSanctionedPayee(payee string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sanctioned[payee] = struct{}{}
}

func (e *Engine) RemoveSanctionedPayee(payee string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.sanctioned, payee)
}

// checkClock 校验并推进全局时钟。调用方必须持有 mu。
// 时钟只随“被接受”的操作推进：返回错误的调用方不得再触碰 lastNow。
func (e *Engine) checkClock(now int64) error {
	if now < e.lastNow {
		return newError(ErrCodeClockBackward,
			"clock moved backwards: now=%d < lastAccepted=%d", now, e.lastNow)
	}
	return nil
}

// materializeSender 把某汇款人 deadline < now 的待审核汇款物化为逾期失败。
// 失败生效时刻记为 deadline+1（逾期事实成立的第一秒）：不依赖墙钟、
// 不依赖任何后台任务，同一操作序列重放结果完全一致。
//
// 每笔待审核汇款只被物化一次，摊销 O(1)；与历史汇款总数无关。
func (e *Engine) materializeSender(a *account, now int64) {
	failed := a.materialize(now)
	for _, t := range failed {
		a.release(t.day, t.occupied)
	}
}
