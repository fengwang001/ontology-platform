package idb

// TxState 是事务生命周期状态。
type TxState int

const (
	TxPending  TxState = iota // 已创建，尚未开始
	TxActive                  // 已开始，可以接受并执行请求
	TxFinished                // 已结束（提交 / 中止 / 被取消）
)

func (s TxState) String() string {
	switch s {
	case TxPending:
		return "pending"
	case TxActive:
		return "active"
	default:
		return "finished"
	}
}

// Outcome 是已结束事务的结局。
type Outcome int

const (
	OutcomeNone Outcome = iota
	OutcomeCommitted
	OutcomeAborted
	OutcomeCancelled
)

func (o Outcome) String() string {
	switch o {
	case OutcomeCommitted:
		return "committed"
	case OutcomeAborted:
		return "aborted"
	case OutcomeCancelled:
		return "cancelled"
	default:
		return "none"
	}
}

// Options 控制单个请求的行为。
type Options struct {
	AddOnly     bool // 仅添加：键已存在时按键冲突拒绝
	IgnoreError bool // 请求失败时不中止事务
}

type requestOp int

const (
	opGet requestOp = iota
	opPut
)

// Request 是事务内的一次仓库操作。
// 回调（若提供）在请求完成后于锁外执行，回调内可以继续提交新请求。
type Request struct {
	op        requestOp
	tx        *Tx
	store     string
	key       string
	value     []byte
	addOnly   bool
	ignoreErr bool
	cb        func(*Request)

	done   bool
	err    error
	result []byte
	found  bool
}

func (r *Request) Done() bool {
	r.tx.kernel.mu.Lock()
	defer r.tx.kernel.mu.Unlock()
	return r.done
}

func (r *Request) Err() error {
	r.tx.kernel.mu.Lock()
	defer r.tx.kernel.mu.Unlock()
	return r.err
}

// Value 返回 Get 的结果；found=false 表示键不存在（不是错误）。
func (r *Request) Value() ([]byte, bool) {
	r.tx.kernel.mu.Lock()
	defer r.tx.kernel.mu.Unlock()
	return r.result, r.found
}

// Tx 是一个普通事务（只读或读写）。
type Tx struct {
	id       int64
	kernel   *Kernel
	db       *Database
	conn     *Connection
	mode     Mode
	scope    map[string]bool
	state    TxState
	outcome  Outcome
	snapshot uint64

	overlay    map[string]map[string][]byte // 读写事务的私有写缓冲
	queue      []*Request                   // 开始前到达的请求
	reqCount   int
	pendingCbs int
}

func (t *Tx) ID() int64 { return t.id }

func (t *Tx) State() TxState {
	t.kernel.mu.Lock()
	defer t.kernel.mu.Unlock()
	return t.state
}

func (t *Tx) Outcome() Outcome {
	t.kernel.mu.Lock()
	defer t.kernel.mu.Unlock()
	return t.outcome
}

// Get 读取 store 中的 key；键不存在时 found=false，不算错误。
func (t *Tx) Get(store string, key []byte, opts *Options, cb func(*Request)) *Request {
	r := &Request{op: opGet, store: store, key: string(key), cb: cb}
	if opts != nil {
		r.ignoreErr = opts.IgnoreError
	}
	return t.submit(r)
}

// Put 写入 store 中的 key；opts.AddOnly 时遇已有键按键冲突拒绝。
func (t *Tx) Put(store string, key, value []byte, opts *Options, cb func(*Request)) *Request {
	r := &Request{op: opPut, store: store, key: string(key), value: value, cb: cb}
	if opts != nil {
		r.addOnly = opts.AddOnly
		r.ignoreErr = opts.IgnoreError
	}
	return t.submit(r)
}

func (t *Tx) submit(r *Request) *Request {
	k := t.kernel
	r.tx = t
	k.mu.Lock()
	k.submitLocked(t, r)
	k.mu.Unlock()
	k.finish()
	return r
}

// Commit 显式提交，与自动提交等价；对已结束事务按事务已结束拒绝。
func (t *Tx) Commit() error {
	k := t.kernel
	k.mu.Lock()
	if t.state == TxFinished {
		k.mu.Unlock()
		return errf(KindTxFinished, "tx %d already finished", t.id)
	}
	k.commitTxLocked(t)
	k.mu.Unlock()
	k.finish()
	return nil
}

// Abort 显式中止，丢弃全部未提交写入。
func (t *Tx) Abort() error {
	k := t.kernel
	k.mu.Lock()
	if t.state == TxFinished {
		k.mu.Unlock()
		return errf(KindTxFinished, "tx %d already finished", t.id)
	}
	k.finishTxLocked(t, OutcomeAborted)
	k.mu.Unlock()
	k.finish()
	return nil
}

// UpgradeTx 是版本变更事务句柄，仅在 Open 的 onUpgrade 回调执行期间有效。
type UpgradeTx struct {
	k  *Kernel
	db *Database
}

// CreateStore 创建对象仓库，立即可用。
func (u *UpgradeTx) CreateStore(name string) error {
	u.k.mu.Lock()
	defer u.k.mu.Unlock()
	if name == "" {
		return errf(KindInvalidArg, "empty store name")
	}
	if !u.db.vcInProgress {
		return errf(KindInvalidState, "upgrade transaction is not active")
	}
	if _, ok := u.db.vcStores[name]; ok {
		return errf(KindInvalidState, "store %q already exists", name)
	}
	u.db.vcStores[name] = newStoreData()
	return nil
}

// DeleteStore 删除对象仓库；同一事务内后续访问按仓库不存在拒绝。
func (u *UpgradeTx) DeleteStore(name string) error {
	u.k.mu.Lock()
	defer u.k.mu.Unlock()
	if name == "" {
		return errf(KindInvalidArg, "empty store name")
	}
	if !u.db.vcInProgress {
		return errf(KindInvalidState, "upgrade transaction is not active")
	}
	if _, ok := u.db.vcStores[name]; !ok {
		return errf(KindStoreNotFound, "store %q does not exist", name)
	}
	delete(u.db.vcStores, name)
	return nil
}

// Abort 中止版本变更：版本号与仓库集合恢复到之前状态。
func (u *UpgradeTx) Abort() {
	u.k.mu.Lock()
	u.db.vcAborted = true
	u.k.mu.Unlock()
}
