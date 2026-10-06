package idb

// Connection 是一个打开的数据库连接。
// OnVersionChange 在版本变更事务或删除数据库被本连接阻塞时触发，
// 应在打开新事务之前设置；回调在锁外执行，通常应在其中调用 Close。
type Connection struct {
	k   *Kernel
	db  *Database
	txs map[*Tx]struct{} // 本连接上未结束的事务（排队 + 运行中）

	closing bool
	closed  bool // 所有已开始的事务完成后才置位

	OnVersionChange func()
}

func newConnection(k *Kernel, db *Database) *Connection {
	return &Connection{k: k, db: db, txs: map[*Tx]struct{}{}}
}

// CreateTx 创建事务：校验参数、连接状态与作用域后进入调度队列。
// 拒绝次序：参数非法 > 状态不允许（关闭期间） > 仓库不存在。
func (c *Connection) CreateTx(mode Mode, scope []string) (*Tx, error) {
	k := c.k
	k.mu.Lock()
	if len(scope) == 0 {
		k.mu.Unlock()
		return nil, errf(KindInvalidArg, "empty transaction scope")
	}
	scopeSet := make(map[string]bool, len(scope))
	for _, name := range scope {
		if name == "" {
			k.mu.Unlock()
			return nil, errf(KindInvalidArg, "empty store name in scope")
		}
		scopeSet[name] = true
	}
	if c.closing || c.closed {
		k.mu.Unlock()
		return nil, errf(KindInvalidState, "connection is closing")
	}
	for name := range scopeSet {
		if _, ok := c.db.stores[name]; !ok {
			k.mu.Unlock()
			return nil, errf(KindStoreNotFound, "store %q does not exist", name)
		}
	}
	k.nextTxID++
	tx := &Tx{
		id:     k.nextTxID,
		kernel: k,
		db:     c.db,
		conn:   c,
		mode:   mode,
		scope:  scopeSet,
		state:  TxPending,
	}
	c.txs[tx] = struct{}{}
	c.db.sched.pending = append(c.db.sched.pending, tx)
	k.scheduleLocked(c.db)
	k.mu.Unlock()
	k.finish()
	return tx, nil
}

// Close 关闭连接：尚未开始的事务按被取消失败，
// 已开始的事务允许完成后连接才真正关闭；关闭期间不得创建新事务。
func (c *Connection) Close() {
	k := c.k
	k.mu.Lock()
	if c.closing || c.closed {
		k.mu.Unlock()
		return
	}
	c.closing = true
	// 先快照再批量取消：取消过程中不调度，
	// 保证"关闭调用时尚未开始的事务"全部被取消，而不会中途被启动。
	var pending []*Tx
	for tx := range c.txs {
		if tx.state == TxPending {
			pending = append(pending, tx)
		}
	}
	for _, tx := range pending {
		k.cancelPendingLocked(tx)
	}
	k.scheduleLocked(c.db)
	k.checkConnLocked(c)
	k.mu.Unlock()
	k.finish()
}

// Closing 报告 Close 是否已被调用。
func (c *Connection) Closing() bool {
	c.k.mu.Lock()
	defer c.k.mu.Unlock()
	return c.closing
}

// FullyClosed 报告连接是否已真正关闭（其已开始的事务全部结束）。
func (c *Connection) FullyClosed() bool {
	c.k.mu.Lock()
	defer c.k.mu.Unlock()
	return c.closed
}
