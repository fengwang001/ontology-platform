package ontology

import "sync"

// Loss 为一次损失登记信息。金额单位：分。
type Loss struct {
	LossID  string
	Insured string
	Day     int
	Amount  int64
}

// Engine 为分摊赔付引擎。不同被保人的数据互不干扰，可并发操作。
type Engine struct {
	mu     sync.Mutex
	books  map[string]*insuredBook
	losses map[string]string // 损失号 -> 被保人，保证全引擎损失号唯一
	shards [64]sync.Mutex    // 按被保人分片：不同被保人操作真正并行
}

func NewEngine() *Engine {
	return &Engine{
		books:  map[string]*insuredBook{},
		losses: map[string]string{},
	}
}

func (e *Engine) shard(insured string) *sync.Mutex {
	h := uint64(1469598103934665603)
	for i := 0; i < len(insured); i++ {
		h ^= uint64(insured[i])
		h *= 1099511628211
	}
	return &e.shards[h%uint64(len(e.shards))]
}

// getOrCreateBook 仅在全局注册阶段调用（先全局锁后分片锁，无死锁）。
func (e *Engine) getOrCreateBook(insured string) *insuredBook {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.books[insured]
	if !ok {
		b = newBook()
		e.books[insured] = b
	}
	return b
}

func (e *Engine) getBook(insured string) *insuredBook {
	e.mu.Lock()
	b := e.books[insured]
	e.mu.Unlock()
	return b
}

// RegisterPolicy 登记保单。
func (e *Engine) RegisterPolicy(p Policy) error {
	if err := validatePolicy(p); err != nil {
		return err
	}
	lock := e.shard(p.Insured)
	lock.Lock()
	defer lock.Unlock()

	b := e.getOrCreateBook(p.Insured)
	if _, dup := b.policies[p.PolicyID]; dup {
		return errCode(ErrPolicyDuplicate)
	}
	b.add(p)
	return nil
}

// AcceptLoss 受理损失并立即扣减各保单年度累计限额。
func (e *Engine) AcceptLoss(l Loss) (*ApportionResult, error) {
	if err := validateLoss(l); err != nil {
		return nil, err
	}
	lock := e.shard(l.Insured)
	lock.Lock()
	defer lock.Unlock()

	b := e.getBook(l.Insured)
	if b == nil {
		return nil, errCode(ErrInsuredMissing)
	}
	e.mu.Lock()
	if _, exists := e.losses[l.LossID]; exists {
		e.mu.Unlock()
		return nil, errCode(ErrLossExists)
	}
	e.mu.Unlock()

	result := apportion(b.covering(l.Day), l.Day, l.Amount)
	pays := make(map[string]int64, len(result.Shares))
	for id, v := range result.Shares {
		pays[id] = v
	}
	b.apply(l, pays, result.NoPayer)

	e.mu.Lock()
	e.losses[l.LossID] = l.Insured
	e.mu.Unlock()
	return &result, nil
}

// CancelLoss 撤销该被保人名下受理次序最后的一笔损失。
func (e *Engine) CancelLoss(insured, lossID string) error {
	if insured == "" || lossID == "" {
		return errCode(ErrInvalid)
	}
	lock := e.shard(insured)
	lock.Lock()
	defer lock.Unlock()

	b := e.getBook(insured)
	if b == nil {
		return errCode(ErrInsuredMissing)
	}
	e.mu.Lock()
	if _, exists := e.losses[lossID]; !exists {
		e.mu.Unlock()
		return errCode(ErrLossMissing)
	}
	e.mu.Unlock()

	n := len(b.losses)
	if b.losses[n-1].lossID != lossID {
		return errCode(ErrNotLast)
	}
	b.undoLast()

	e.mu.Lock()
	delete(e.losses, lossID)
	e.mu.Unlock()
	return nil
}

// Balance 查询保单年度累计剩余额。
func (e *Engine) Balance(insured, policyID string) (int64, bool, error) {
	if insured == "" || policyID == "" {
		return 0, false, errCode(ErrInvalid)
	}
	lock := e.shard(insured)
	lock.Lock()
	defer lock.Unlock()
	b := e.getBook(insured)
	if b == nil {
		return 0, false, errCode(ErrInsuredMissing)
	}
	v, ok := b.balance(policyID)
	return v, ok, nil
}

// LossPaid 查询已受理损失的应赔总额。
func (e *Engine) LossPaid(lossID string) (int64, bool) {
	if lossID == "" {
		return 0, false
	}
	e.mu.Lock()
	insured, ok := e.losses[lossID]
	e.mu.Unlock()
	if !ok {
		return 0, false
	}
	lock := e.shard(insured)
	lock.Lock()
	defer lock.Unlock()
	b := e.getBook(insured)
	var paid int64
	for _, v := range b.losses[b.lossSet[lossID]].pays {
		paid += v
	}
	return paid, true
}
