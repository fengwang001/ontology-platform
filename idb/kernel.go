package idb

import (
	"maps"
	"sync"
)

// Kernel 是事务作用域调度内核：数据库注册表 + 全局互斥锁。
// 所有公开操作在锁内完成状态变更，等价于某个串行顺序；
// 用户回调（请求回调、版本变更通知、升级回调）统一在锁外执行。
type Kernel struct {
	mu  sync.Mutex
	dbs map[string]*Database

	cbQueue   []func() // 待执行的锁外回调
	touched   []*Tx    // 自上次提交检查以来有请求推进的事务
	finishing bool
	nextTxID  int64

	// 可验证的性能计数器（测试用来证明复杂度性质）。
	overlapChecks int64 // 调度判定时做的重叠检查次数
	commitKeyOps  int64 // 提交时逐键追加版本的次数
}

func NewKernel() *Kernel {
	return &Kernel{dbs: map[string]*Database{}}
}

// Database 以名字标识，带整数版本与对象仓库集合。
type Database struct {
	name      string
	version   int
	stores    map[string]*storeData
	commitSeq uint64

	conns map[*Connection]struct{}
	queue []queueEntry // 打开 / 删除请求，按到达序处理
	sched scheduler

	vcInProgress bool
	vcStores     map[string]*storeData
	vcAborted    bool
	notifiedFor  queueEntry
}

type queueEntry interface{ queueEntry() }

// OpenRequest 是一次打开请求的句柄；阻塞的请求在条件满足后完成。
type OpenRequest struct {
	k         *Kernel
	version   int
	onUpgrade func(*UpgradeTx)

	done bool
	conn *Connection
	err  error
}

func (*OpenRequest) queueEntry() {}

func (r *OpenRequest) Done() bool {
	r.k.mu.Lock()
	defer r.k.mu.Unlock()
	return r.done
}

// Result 返回打开结果；仅在 Done 后有意义。
func (r *OpenRequest) Result() (*Connection, error) {
	r.k.mu.Lock()
	defer r.k.mu.Unlock()
	return r.conn, r.err
}

// DeleteRequest 是一次删除数据库请求的句柄。
type DeleteRequest struct {
	k    *Kernel
	done bool
	err  error
}

func (*DeleteRequest) queueEntry() {}

func (r *DeleteRequest) Done() bool {
	r.k.mu.Lock()
	defer r.k.mu.Unlock()
	return r.done
}

func (r *DeleteRequest) Err() error {
	r.k.mu.Lock()
	defer r.k.mu.Unlock()
	return r.err
}

// Open 打开数据库。请求版本高于当前版本时进入版本变更流程
// （onUpgrade 在版本变更事务内执行，可创建/删除仓库，可 Abort 回滚）；
// 等于当前版本直接打开；低于当前版本按版本过低拒绝。
// 版本变更被既有连接阻塞时，之后的打开请求一律排在其后。
func (k *Kernel) Open(name string, version int, onUpgrade func(*UpgradeTx)) *OpenRequest {
	req := &OpenRequest{k: k, version: version, onUpgrade: onUpgrade}
	k.mu.Lock()
	if name == "" || version < 1 {
		req.done = true
		req.err = errf(KindInvalidArg, "invalid database name %q or version %d", name, version)
		k.mu.Unlock()
		return req
	}
	db := k.dbs[name]
	if db == nil {
		db = &Database{
			name:   name,
			stores: map[string]*storeData{},
			conns:  map[*Connection]struct{}{},
			sched:  newScheduler(),
		}
		k.dbs[name] = db
	}
	db.queue = append(db.queue, req)
	k.processQueueLocked(db)
	k.mu.Unlock()
	k.finish()
	return req
}

// DeleteDatabase 删除数据库：要求无打开连接，否则通知并阻塞，
// 所有连接关闭后删除生效，此后该名字的版本视为零。
func (k *Kernel) DeleteDatabase(name string) *DeleteRequest {
	req := &DeleteRequest{k: k}
	k.mu.Lock()
	if name == "" {
		req.done = true
		req.err = errf(KindInvalidArg, "empty database name")
		k.mu.Unlock()
		return req
	}
	db := k.dbs[name]
	if db == nil {
		req.done = true // 不存在的数据库版本本为零，删除立即生效
		k.mu.Unlock()
		return req
	}
	db.queue = append(db.queue, req)
	k.processQueueLocked(db)
	k.mu.Unlock()
	k.finish()
	return req
}

// Version 返回数据库当前版本；不存在（含已删除）时为零。
func (k *Kernel) Version(name string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if db := k.dbs[name]; db != nil {
		return db.version
	}
	return 0
}

// Snapshot 返回当前版本号与全部已提交数据（测试与调试用）。
func (k *Kernel) Snapshot(name string) (int, map[string]map[string][]byte) {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := map[string]map[string][]byte{}
	db := k.dbs[name]
	if db == nil {
		return 0, out
	}
	for storeName, sd := range db.stores {
		m := map[string][]byte{}
		for key := range sd.data {
			if v, ok := sd.get(key, ^uint64(0)); ok {
				m[key] = v
			}
		}
		out[storeName] = m
	}
	return db.version, out
}

// stats 暴露调度器内部规模与计数器，用于性能性质的白盒验证。
type stats struct {
	pending, running            int
	overlapChecks, commitKeyOps int64
}

func (k *Kernel) statsFor(name string) stats {
	k.mu.Lock()
	defer k.mu.Unlock()
	var s stats
	if db := k.dbs[name]; db != nil {
		s.pending = len(db.sched.pending)
		s.running = len(db.sched.running)
	}
	s.overlapChecks = k.overlapChecks
	s.commitKeyOps = k.commitKeyOps
	return s
}

// ---------- 请求处理 ----------

// submitLocked 按规定的拒绝次序校验请求，然后排队或执行。
// 被拒绝的请求立即完成且不影响事务状态与队列。
func (k *Kernel) submitLocked(t *Tx, r *Request) {
	switch {
	case r.store == "":
		k.rejectLocked(t, r, errf(KindInvalidArg, "empty store name"))
	case r.key == "":
		k.rejectLocked(t, r, errf(KindInvalidArg, "empty key"))
	case r.op == opPut && t.mode == ReadOnly:
		k.rejectLocked(t, r, errf(KindInvalidState, "put on read-only tx %d", t.id))
	default:
		if _, ok := t.db.stores[r.store]; !ok {
			k.rejectLocked(t, r, errf(KindStoreNotFound, "store %q does not exist", r.store))
			return
		}
		if !t.scope[r.store] {
			k.rejectLocked(t, r, errf(KindScopeMismatch, "store %q not in scope of tx %d", r.store, t.id))
			return
		}
		if t.state == TxFinished {
			k.rejectLocked(t, r, errf(KindTxFinished, "tx %d already finished", t.id))
			return
		}
		if t.state == TxPending {
			t.queue = append(t.queue, r)
			return
		}
		k.executeLocked(t, r)
	}
}

func (k *Kernel) rejectLocked(t *Tx, r *Request, err error) {
	r.done = true
	r.err = err
	k.queueRequestCbLocked(t, r)
}

// executeLocked 在运行中的事务上执行请求。
// 键冲突是运行期失败：除非请求标记 IgnoreError，否则事务中止。
func (k *Kernel) executeLocked(t *Tx, r *Request) {
	t.reqCount++
	r.done = true
	switch r.op {
	case opGet:
		if vs, ok := t.overlay[r.store]; ok {
			if v, ok := vs[r.key]; ok {
				r.result, r.found = v, true
				break
			}
		}
		r.result, r.found = t.db.stores[r.store].get(r.key, t.snapshot)
	case opPut:
		if r.addOnly {
			_, inOverlay := t.overlay[r.store][r.key]
			_, inStore := t.db.stores[r.store].get(r.key, t.snapshot)
			if inOverlay || inStore {
				r.err = errf(KindKeyConflict, "key already exists in store %q", r.store)
				k.queueRequestCbLocked(t, r)
				if r.ignoreErr {
					k.touchLocked(t)
				} else {
					k.finishTxLocked(t, OutcomeAborted)
				}
				return
			}
		}
		if t.overlay == nil {
			t.overlay = map[string]map[string][]byte{}
		}
		if t.overlay[r.store] == nil {
			t.overlay[r.store] = map[string][]byte{}
		}
		t.overlay[r.store][r.key] = r.value
	}
	k.queueRequestCbLocked(t, r)
	k.touchLocked(t)
}

// queueRequestCbLocked 把请求回调排入锁外回调队列。
func (k *Kernel) queueRequestCbLocked(t *Tx, r *Request) {
	if r.cb == nil {
		return
	}
	t.pendingCbs++
	k.cbQueue = append(k.cbQueue, func() {
		r.cb(r)
		k.mu.Lock()
		t.pendingCbs--
		k.touchLocked(t)
		k.mu.Unlock()
	})
}

func (k *Kernel) touchLocked(t *Tx) {
	k.touched = append(k.touched, t)
}

// ---------- 事务结束与调度 ----------

// commitTxLocked 提交事务：读写事务的全部写入在此刻一次性可见。
// 提交只按本事务写入的键逐个追加版本，开销与仓库中无关键数无关。
func (k *Kernel) commitTxLocked(t *Tx) {
	if t.state == TxActive && len(t.overlay) > 0 {
		db := t.db
		db.commitSeq++
		for storeName, kvs := range t.overlay {
			sd := db.stores[storeName]
			for key, val := range kvs {
				sd.put(key, db.commitSeq, val)
				k.commitKeyOps++
			}
		}
	}
	k.finishTxLocked(t, OutcomeCommitted)
}

func (k *Kernel) finishTxLocked(t *Tx, outcome Outcome) {
	if t.state == TxFinished {
		return
	}
	if t.state == TxPending {
		t.db.sched.removePending(t)
	} else {
		delete(t.db.sched.running, t)
	}
	t.state = TxFinished
	t.outcome = outcome
	delete(t.conn.txs, t)
	for _, r := range t.queue {
		r.done = true
		r.err = errf(KindCancelled, "tx %d ended before queued request ran", t.id)
		k.queueRequestCbLocked(t, r)
	}
	t.queue = nil
	t.overlay = nil
	k.checkConnLocked(t.conn)
	k.scheduleLocked(t.db)
}

// cancelPendingLocked 取消一个尚未开始的事务，不触发调度；
// 供连接关闭时批量取消使用，由调用方最后统一调度。
func (k *Kernel) cancelPendingLocked(t *Tx) {
	t.db.sched.removePending(t)
	t.state = TxFinished
	t.outcome = OutcomeCancelled
	delete(t.conn.txs, t)
	for _, r := range t.queue {
		r.done = true
		r.err = errf(KindCancelled, "tx %d cancelled before it started", t.id)
		k.queueRequestCbLocked(t, r)
	}
	t.queue = nil
}

func (k *Kernel) scheduleLocked(db *Database) {
	for _, t := range db.sched.computeStarts(k) {
		k.startTxLocked(db, t)
	}
}

// startTxLocked 在开始时刻确定快照；排队请求由提交检查逐个执行，
// 保证每个请求的回调链完整跑完后才执行下一个排队请求。
func (k *Kernel) startTxLocked(db *Database, t *Tx) {
	t.state = TxActive
	t.snapshot = db.commitSeq
	if len(t.queue) > 0 {
		k.touchLocked(t)
	}
}

// checkConnLocked 在连接关闭中且其事务全部结束时真正关闭连接。
func (k *Kernel) checkConnLocked(c *Connection) {
	if c.closing && !c.closed && len(c.txs) == 0 {
		c.closed = true
		delete(c.db.conns, c)
		k.processQueueLocked(c.db)
	}
}

// ---------- 打开 / 删除队列 ----------

// processQueueLocked 按到达序处理队首请求；队首无法推进时整个队列等待，
// 这保证"版本变更通知后新的打开请求一律排在该版本变更之后"。
func (k *Kernel) processQueueLocked(db *Database) {
	for len(db.queue) > 0 {
		head := db.queue[0]
		switch req := head.(type) {
		case *OpenRequest:
			switch {
			case req.version < db.version:
				k.popHeadLocked(db)
				req.done = true
				req.err = errf(KindVersionTooLow, "requested version %d below current %d", req.version, db.version)
			case req.version > db.version:
				if db.vcInProgress {
					return
				}
				if len(db.conns) > 0 {
					k.notifyLocked(db, head)
					return
				}
				k.popHeadLocked(db)
				db.vcInProgress = true
				db.vcStores = maps.Clone(db.stores)
				db.vcAborted = false
				k.cbQueue = append(k.cbQueue, func() { k.runUpgrade(db, req) })
				return
			default: // 等于当前版本，直接打开
				if db.vcInProgress {
					return
				}
				k.popHeadLocked(db)
				conn := newConnection(k, db)
				db.conns[conn] = struct{}{}
				req.done = true
				req.conn = conn
			}
		case *DeleteRequest:
			if db.vcInProgress || len(db.conns) > 0 {
				k.notifyLocked(db, head)
				return
			}
			k.popHeadLocked(db)
			delete(k.dbs, db.name)
			req.done = true
		}
	}
}

func (k *Kernel) popHeadLocked(db *Database) {
	head := db.queue[0]
	db.queue[0] = nil
	db.queue = db.queue[1:]
	if db.notifiedFor == head {
		db.notifiedFor = nil
	}
}

// notifyLocked 对当前所有打开连接发出版本变更通知（每个阻塞源头只通知一次）。
func (k *Kernel) notifyLocked(db *Database, head queueEntry) {
	if db.notifiedFor == head {
		return
	}
	db.notifiedFor = head
	for c := range db.conns {
		c := c
		if c.OnVersionChange != nil {
			k.cbQueue = append(k.cbQueue, func() { c.OnVersionChange() })
		}
	}
}

// runUpgrade 在锁外执行升级回调，然后提交或回滚版本变更事务。
func (k *Kernel) runUpgrade(db *Database, req *OpenRequest) {
	utx := &UpgradeTx{k: k, db: db}
	panicked := false
	if req.onUpgrade != nil {
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			req.onUpgrade(utx)
		}()
	}
	k.mu.Lock()
	if db.vcAborted || panicked {
		// 回滚：版本与仓库集合保持之前状态（vcStores 直接丢弃）。
		db.vcInProgress = false
		db.vcStores = nil
		db.vcAborted = false
		req.done = true
		req.err = errf(KindAborted, "upgrade to version %d aborted", req.version)
	} else {
		db.stores = db.vcStores
		db.version = req.version
		db.vcStores = nil
		db.vcInProgress = false
		conn := newConnection(k, db)
		db.conns[conn] = struct{}{}
		req.done = true
		req.conn = conn
	}
	k.processQueueLocked(db)
	k.mu.Unlock()
}

// ---------- 锁外回调与自动提交 ----------

// finish 是公开操作的收尾：先在锁外跑完所有回调，再做自动提交检查，
// 直到既没有待执行回调也没有可提交事务为止。
func (k *Kernel) finish() {
	k.mu.Lock()
	if k.finishing {
		k.mu.Unlock()
		return
	}
	k.finishing = true
	k.mu.Unlock()
	for {
		k.runCallbacks()
		k.mu.Lock()
		k.commitCheckLocked()
		done := len(k.cbQueue) == 0
		k.mu.Unlock()
		if done {
			k.mu.Lock()
			k.finishing = false
			k.mu.Unlock()
			return
		}
	}
}

func (k *Kernel) runCallbacks() {
	for {
		k.mu.Lock()
		if len(k.cbQueue) == 0 {
			k.mu.Unlock()
			return
		}
		cb := k.cbQueue[0]
		k.cbQueue[0] = nil
		k.cbQueue = k.cbQueue[1:]
		k.mu.Unlock()
		cb()
	}
}

// commitCheckLocked 推进排队请求并自动提交：
// 运行中的事务若还有排队请求且回调均已返回，则执行下一个；
// 否则在没有未完成请求时提交。
func (k *Kernel) commitCheckLocked() {
	for len(k.touched) > 0 {
		txs := k.touched
		k.touched = nil
		for _, t := range txs {
			if t.state != TxActive || t.pendingCbs > 0 {
				continue
			}
			if len(t.queue) > 0 {
				r := t.queue[0]
				t.queue = t.queue[1:]
				k.executeLocked(t, r)
				continue
			}
			if t.reqCount > 0 {
				k.commitTxLocked(t)
			}
		}
	}
}
