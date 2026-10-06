package idb

// Connection 是打开的数据库连接。
type Connection struct {
	db      *database
	version int
	closing bool
	closed  bool
	upgrade bool // 承载版本变更事务的连接
	txns    map[*Transaction]struct{}

	// OnVersionChange：其他连接请求版本变更/删除时收到通知。
	OnVersionChange func()

	closeDone chan struct{}
}

// Transaction 在连接上创建普通事务（只读/读写）。
func (c *Connection) Transaction(mode Mode, stores []string) (*Transaction, error) {
	if mode != ReadOnly && mode != ReadWrite {
		return nil, newError(KindInvalidArgument, "normal transaction mode must be read-only or read-write")
	}
	k := c.db.k
	k.mu.Lock()
	defer k.mu.Unlock()

	// 拒绝次序：参数非法先于状态不允许（关闭期间创建事务）。
	if len(stores) == 0 {
		return nil, newError(KindInvalidArgument, "transaction scope must be non-empty")
	}
	if c.closing || c.closed {
		return nil, newError(KindInvalidState, "connection is closing; new transactions are not allowed")
	}
	if c.db.blocker != nil || c.db.upgradeConn != nil {
		return nil, newError(KindInvalidState, "database is undergoing a version change")
	}

	scope := make(map[string]struct{}, len(stores))
	// 去重；仓库存在性逐个检查（仓库不存在先于作用域不符：此处不存在即拒绝）。
	for _, name := range stores {
		if name == "" {
			return nil, newError(KindInvalidArgument, "object store name must be non-empty")
		}
		if !c.db.hasStore(name) {
			return nil, newError(KindNoObjectStore, "object store %q does not exist", name)
		}
		scope[name] = struct{}{}
	}

	t := newTransaction(c.db, c, mode, scope)
	c.txns[t] = struct{}{}
	c.db.sched.register(t)
	k.debugf("conn(v=%d) create tx#%d mode=%v scope=%v -> queued",
		c.version, t.id, mode, stores)
	k.kick()
	return t, nil
}

// Close 请求关闭连接：未开始事务被取消，已开始事务放行完成。
func (c *Connection) Close() {
	k := c.db.k
	k.mu.Lock()
	if c.closed {
		k.mu.Unlock()
		return
	}
	c.closing = true
	k.debugf("conn(v=%d) close requested, %d txns", c.version, len(c.txns))
	for t := range c.txns {
		if t.state == Queued {
			// 尚未开始的事务按被取消失败。
			t.abortLocked(newError(KindCanceled, "transaction canceled because connection is closing"))
		}
	}
	k.checkFinishedClose(c)
	k.kick()
	k.mu.Unlock()
}

// Closed 在连接真正关闭（已开始事务全部完成）后关闭。
func (c *Connection) Closed() <-chan struct{} { return c.closeDone }

func (c *Connection) Version() int {
	c.db.k.mu.Lock()
	v := c.version
	c.db.k.mu.Unlock()
	return v
}

// CreateObjectStore/DeleteObjectStore 仅允许在版本变更事务内调用。
func (c *Connection) CreateObjectStore(name string) error {
	k := c.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if name == "" {
		return newError(KindInvalidArgument, "object store name must be non-empty")
	}
	vt := c.versionChangeTxLocked()
	if vt == nil {
		return newError(KindInvalidState, "createObjectStore is allowed only inside a version change transaction")
	}
	if vt.state != Running {
		return newError(KindTxInactive, "version change transaction has already finished")
	}
	if c.db.hasStore(name) {
		return newError(KindConstraint, "object store %q already exists", name)
	}
	c.db.createStore(name)
	vt.scope[name] = struct{}{}
	k.debugf("tx#%d createStore %q -> stores=%v", vt.id, name, c.db.storeNames())
	return nil
}

func (c *Connection) DeleteObjectStore(name string) error {
	k := c.db.k
	k.mu.Lock()
	defer k.mu.Unlock()
	if name == "" {
		return newError(KindInvalidArgument, "object store name must be non-empty")
	}
	vt := c.versionChangeTxLocked()
	if vt == nil {
		return newError(KindInvalidState, "deleteObjectStore is allowed only inside a version change transaction")
	}
	if vt.state != Running {
		return newError(KindTxInactive, "version change transaction has already finished")
	}
	if !c.db.hasStore(name) {
		return newError(KindNoObjectStore, "object store %q does not exist", name)
	}
	c.db.deleteStore(name)
	delete(vt.scope, name)
	k.debugf("tx#%d deleteStore %q -> stores=%v", vt.id, name, c.db.storeNames())
	return nil
}

// VersionChange 返回该连接的版本变更事务（仅 upgrade 连接非 nil）。
func (c *Connection) VersionChange() *Transaction {
	c.db.k.mu.Lock()
	t := c.versionChangeTxLocked()
	c.db.k.mu.Unlock()
	return t
}

func (c *Connection) versionChangeTxLocked() *Transaction {
	if !c.upgrade {
		return nil
	}
	for t := range c.txns {
		if t.mode == VersionChange {
			return t
		}
	}
	return nil
}
