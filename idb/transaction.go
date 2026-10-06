package idb

import (
	"bytes"
	"sort"
	"sync"
)

// Mode 为事务模式。
type Mode int

const (
	ReadOnly Mode = iota
	ReadWrite
	VersionChange
)

// State 为事务生命周期状态。
type State int

const (
	Queued  State = iota // 已创建，等待调度开始
	Running              // 已开始，可发请求
	Committed
	Aborted
)

// Request 是事务内的一次仓库请求，结果通过 OnSuccess / OnError 回调投递。
// 回调由内核的单一投递循环按请求创建序调用；回调中可以继续发请求，
// 从而等价于 IDB“完成事件 → 微任务自动提交”的确定时序。
type Request struct {
	// Ignorable 为 true 时，该请求失败只投递错误，不中止事务。
	Ignorable bool
	OnSuccess func(res *RequestResult)
	OnError   func(err *Error)

	lastErr *Error

	store   string
	key     []byte
	value   []byte
	addOnly bool
	kind    reqKind
}

type reqKind int

const (
	reqGet reqKind = iota
	reqPut
	reqDelete
)

// RequestResult 为请求成功结果；Exists=false 表示键不存在（读取不是错误）。
type RequestResult struct {
	Exists bool
	Value  []byte
}

// Transaction 为事务句柄。
type Transaction struct {
	mode  Mode
	scope map[string]struct{}
	state State
	snap  uint64 // 只读：开始时快照；读写：读自身写之前用的基线快照
	db    *database
	conn  *Connection
	id    uint64

	requestedVersion int
	oldVersion       int
	oldStores        map[string]*ObjectStore // 版本变更开始前的深拷贝
	queuedOpens      []*pendingOpen          // 版本变更期间排在其后的打开请求

	writes map[string]map[string]*workValue

	pending int // 在途请求数（holds 也计入）
	holds   int
	queue   []*queuedReq

	abortErr *Error
	finish   chan struct{}
	cond     *sync.Cond

	OnComplete func()
	OnAbort    func(err *Error)
}

type workValue struct {
	value []byte
	alive bool
}

type queuedReq struct{ req *Request }

func newTransaction(db *database, conn *Connection, mode Mode, scope map[string]struct{}) *Transaction {
	t := &Transaction{
		mode: mode, db: db, conn: conn, scope: scope,
		state: Queued, writes: make(map[string]map[string]*workValue),
		finish: make(chan struct{}),
	}
	t.cond = sync.NewCond(&db.k.mu)
	return t
}

// validateRequest 按统一拒绝次序做同步校验：
// 参数非法 → 仓库不存在 → 作用域不符 → 事务已结束。
// 只读事务的写属于状态不允许，在调用处先于本检查执行。
func (t *Transaction) validateRequest(store string, key []byte) *Error {
	if store == "" || len(key) == 0 {
		return newError(KindInvalidArgument,
			"store name must be non-empty and key must be a non-empty byte string")
	}
	if !t.db.hasStore(store) {
		return newError(KindNoObjectStore, "object store %q does not exist", store)
	}
	if _, ok := t.scope[store]; !ok {
		return newError(KindScopeViolation, "store %q is not in the transaction scope", store)
	}
	if t.state == Committed || t.state == Aborted {
		return newError(KindTxInactive, "transaction has already finished")
	}
	return nil
}

func (t *Transaction) checkWritePre(store string, key []byte) *Error {
	if store == "" || len(key) == 0 {
		return newError(KindInvalidArgument,
			"store name must be non-empty and key must be a non-empty byte string")
	}
	if t.mode == ReadOnly {
		return newError(KindInvalidState, "write is not allowed in a read-only transaction")
	}
	return nil
}

func (t *Transaction) issueLocked(r *Request) {
	t.pending++
	t.queue = append(t.queue, &queuedReq{req: r})
	t.db.k.kick()
}

func (t *Transaction) Get(store string, key []byte) (*Request, error) {
	return t.GetEx(store, key)
}

// GetEx 与 Get 相同，但允许在入队前绑定回调选项。
func (t *Transaction) GetEx(store string, key []byte, opts ...RequestOption) (*Request, error) {
	r := &Request{kind: reqGet, store: store, key: bytes.Clone(key)}
	for _, o := range opts {
		o(r)
	}
	return attachGet(t, r, store, key)
}

func attachGet(t *Transaction, r *Request, store string, key []byte) (*Request, error) {
	r.kind = reqGet
	r.store = store
	r.key = bytes.Clone(key)
	k := t.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if e := t.validateRequest(store, key); e != nil {
		return nil, e
	}
	t.issueLocked(r)
	return r, nil
}

func (t *Transaction) Put(store string, key, value []byte, addOnly bool) (*Request, error) {
	return t.PutEx(store, key, value, addOnly)
}

// RequestOption 在请求发出前配置其属性（如可忽略错误）。
type RequestOption func(*Request)

// WithIgnorable 标记该请求的失败为可忽略错误：不中止事务。
func WithIgnorable() RequestOption {
	return func(r *Request) { r.Ignorable = true }
}

// WithOnSuccess / WithOnError 在请求入队前绑定回调，避免与投递循环竞争。
func WithOnSuccess(fn func(*RequestResult)) RequestOption {
	return func(r *Request) { r.OnSuccess = fn }
}

func WithOnError(fn func(*Error)) RequestOption {
	return func(r *Request) { r.OnError = fn }
}

// PutEx 与 Put 相同，但允许附加请求选项（如 WithIgnorable）；
// 选项在请求入队前设置，不存在与投递循环的数据竞争。
func (t *Transaction) PutEx(store string, key, value []byte, addOnly bool, opts ...RequestOption) (*Request, error) {
	r := &Request{
		kind: reqPut, store: store, key: bytes.Clone(key),
		value: bytes.Clone(value), addOnly: addOnly,
	}
	for _, o := range opts {
		o(r)
	}
	return attachPut(t, r, store, key, value, addOnly)
}

func attachPut(t *Transaction, r *Request, store string, key, value []byte, addOnly bool) (*Request, error) {
	r.kind = reqPut
	r.store = store
	r.key = bytes.Clone(key)
	r.value = bytes.Clone(value)
	r.addOnly = addOnly
	k := t.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if e := t.checkWritePre(store, key); e != nil {
		return nil, e
	}
	if e := t.validateRequest(store, key); e != nil {
		return nil, e
	}
	t.issueLocked(r)
	return r, nil
}

func (t *Transaction) Delete(store string, key []byte) (*Request, error) {
	return t.DeleteEx(store, key)
}

// DeleteEx 与 Delete 相同，但允许在入队前绑定回调选项。
func (t *Transaction) DeleteEx(store string, key []byte, opts ...RequestOption) (*Request, error) {
	r := &Request{kind: reqDelete, store: store, key: bytes.Clone(key)}
	for _, o := range opts {
		o(r)
	}
	return attachDelete(t, r, store, key)
}

func attachDelete(t *Transaction, r *Request, store string, key []byte) (*Request, error) {
	r.kind = reqDelete
	r.store = store
	r.key = bytes.Clone(key)
	k := t.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if e := t.checkWritePre(store, key); e != nil {
		return nil, e
	}
	if e := t.validateRequest(store, key); e != nil {
		return nil, e
	}
	t.issueLocked(r)
	return r, nil
}

// Hold/Unhold 显式延长事务，避免请求间隙被自动提交。
func (t *Transaction) Hold() error {
	k := t.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if t.state == Committed || t.state == Aborted {
		return newError(KindTxInactive, "transaction has already finished")
	}
	t.holds++
	t.pending++
	return nil
}

func (t *Transaction) Unhold() error {
	k := t.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if t.holds == 0 {
		return newError(KindInvalidState, "unhold without matching hold")
	}
	t.holds--
	t.pending--
	k.kick()
	return nil
}

func (t *Transaction) Abort() error {
	k := t.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if t.state == Committed || t.state == Aborted {
		return newError(KindTxInactive, "transaction has already finished")
	}
	t.abortLocked(newError(KindCanceled, "transaction explicitly aborted"))
	k.kick()
	return nil
}

// Commit 显式提交。普通事务中它与“无未完成请求时自动提交”等价；
// 版本变更事务没有自动提交，必须显式 Commit（或 Abort）才结束。
func (t *Transaction) Commit() error {
	k := t.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if t.state == Committed || t.state == Aborted {
		return newError(KindTxInactive, "transaction has already finished")
	}
	if t.state == Queued {
		return newError(KindInvalidState, "cannot commit a transaction that has not started")
	}
	if len(t.queue) > 0 || t.pending > 0 {
		return newError(KindInvalidState, "cannot commit while requests are outstanding")
	}
	t.commitLocked()
	k.kick()
	return nil
}

// Wait 阻塞直到事务结束；覆盖“空事务直接等待”的自动提交时机。
func (t *Transaction) Wait() error {
	k := t.db.k
	k.mu.Lock()
	if t.state == Queued || t.state == Running {
		for {
			if t.state == Committed || t.state == Aborted {
				break
			}
			if t.pending == 0 {
				k.kick()
			}
			t.cond.Wait()
		}
	}
	err := t.abortErr
	k.mu.Unlock()
	if err == nil {
		return nil
	}
	return err
}

// waitStart 阻塞到事务离开 Queued；返回 false 表示以中止告终。
func (t *Transaction) waitStart() bool {
	k := t.db.k
	k.mu.Lock()
	for t.state == Queued {
		t.cond.Wait()
	}
	ok := t.state == Running
	k.mu.Unlock()
	return ok
}

func (t *Transaction) Done() <-chan struct{} {
	t.db.k.mu.Lock()
	ch := t.finish
	t.db.k.mu.Unlock()
	return ch
}

func (t *Transaction) State() State {
	t.db.k.mu.Lock()
	s := t.state
	t.db.k.mu.Unlock()
	return s
}

func (t *Transaction) Mode() Mode { return t.mode }

// runOne 执行队首请求（调用时必须 Running）。
func (t *Transaction) runOne() *delivery {
	req := t.queue[0].req
	t.queue = t.queue[1:]

	switch req.kind {
	case reqGet:
		v, ok := t.readVisible(req.store, string(req.key))
		return &delivery{tx: t, req: req,
			res: &RequestResult{Exists: ok, Value: bytes.Clone(v)}}
	case reqPut:
		if req.addOnly && t.keyExists(req.store, string(req.key)) {
			return &delivery{tx: t, req: req, err: newError(KindConstraint,
				"key already exists in store %q (add-only)", req.store)}
		}
		t.stageWrite(req.store, string(req.key), req.value, true)
		return &delivery{tx: t, req: req,
			res: &RequestResult{Exists: true, Value: bytes.Clone(req.value)}}
	case reqDelete:
		t.stageWrite(req.store, string(req.key), nil, false)
		return &delivery{tx: t, req: req, res: &RequestResult{Exists: false}}
	}
	return nil
}

func (t *Transaction) keyExists(store, key string) bool {
	if ws, ok := t.writes[store]; ok {
		if w, ok := ws[key]; ok {
			return w.alive
		}
	}
	_, ok := t.db.stores[store].readAt(key, t.snap)
	return ok
}

func (t *Transaction) readVisible(store, key string) ([]byte, bool) {
	if ws, ok := t.writes[store]; ok {
		if w, ok := ws[key]; ok {
			return w.value, w.alive
		}
	}
	return t.db.stores[store].readAt(key, t.snap)
}

func (t *Transaction) stageWrite(store, key string, value []byte, alive bool) {
	ws, ok := t.writes[store]
	if !ok {
		ws = make(map[string]*workValue)
		t.writes[store] = ws
	}
	ws[key] = &workValue{value: bytes.Clone(value), alive: alive}
}

func (t *Transaction) startLocked() {
	t.state = Running
	t.snap = t.db.sched.commitSeq
	if t.mode == VersionChange {
		t.db.upgradeConn = t.conn
		t.oldVersion = t.db.version
		t.oldStores = t.db.cloneStores()
	}
	t.db.k.debugf("tx#%d start mode=%v scope=%v snap=%d",
		t.id, t.mode, t.sortedScope(), t.snap)
	t.cond.Broadcast() // 唤醒等待事务开始（离开 Queued）的调用方
}

func (t *Transaction) sortedScope() []string {
	names := make([]string, 0, len(t.scope))
	for n := range t.scope {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (t *Transaction) commitLocked() {
	if t.state != Running {
		return
	}
	switch t.mode {
	case VersionChange:
		t.applyWrites()
		t.db.version = t.requestedVersion
		t.db.upgradeConn = nil
		t.db.k.debugf("tx#%d versionchange committed: version %d -> %d stores=%v",
			t.id, t.oldVersion, t.db.version, t.db.storeNames())
	case ReadWrite:
		seq := t.db.sched.nextCommit()
		t.applyWritesAt(seq)
		t.snap = seq
		t.db.k.debugf("tx#%d committed at seq=%d", t.id, seq)
	default:
		t.db.k.debugf("tx#%d readonly committed", t.id)
	}
	t.finishLocked(Committed, nil)
}

func (t *Transaction) abortLocked(err *Error) {
	if t.state == Committed || t.state == Aborted {
		return
	}
	if t.mode == VersionChange && t.state == Running {
		t.db.version = t.oldVersion
		t.db.stores = t.oldStores
		t.db.upgradeConn = nil
		t.db.k.debugf("tx#%d versionchange aborted, restored version=%d stores=%v",
			t.id, t.oldVersion, t.db.storeNames())
	} else {
		t.db.k.debugf("tx#%d aborted: %v", t.id, err)
	}
	for _, q := range t.queue {
		t.db.k.enqueueDelivery(&delivery{
			tx: t, req: q.req,
			err: newError(KindCanceled, "request canceled because transaction aborted"),
		})
	}
	t.queue = nil
	t.pending = 0
	t.finishLocked(Aborted, err)
}

func (t *Transaction) finishLocked(state State, err *Error) {
	t.state = state
	t.abortErr = err
	t.db.sched.unregister(t)
	delete(t.conn.txns, t)
	close(t.finish)
	t.cond.Broadcast()
	t.db.k.enqueueDelivery(&delivery{
		tx: t, terminal: true, complete: state == Committed, err: err,
	})
}

// applyWritesAt 让全部写入在提交点一次性可见：只追加本事务写过的键，
// 开销与本事务写入量成正比，与仓库中无关键数无关。
func (t *Transaction) applyWritesAt(seq uint64) {
	for storeName, ws := range t.writes {
		s := t.db.stores[storeName]
		for k, w := range ws {
			s.append(k, seq, w.value, w.alive)
		}
	}
}

func (t *Transaction) applyWrites() {
	t.applyWritesAt(t.db.sched.nextCommit())
}
