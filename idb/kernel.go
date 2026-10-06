// Package idb 实现一个浏览器内嵌数据库的事务作用域调度内核：
// 数据库版本、对象仓库集合、事务作用域、调度队列与连接生命周期五部分协作，
// 使只读、读写与版本变更事务在重叠作用域、多连接下的开始次序、阻塞关系、
// 提交可见性与失败回滚都可精确复现。
//
// 基本用法：
//
//	k := idb.New(idb.WithLogger(log.New(os.Stderr, "", log.LstdFlags)))
//	defer k.Close()
//
//	// 打开/升级：Open 返回升级连接，版本变更事务在连接上等待开始。
//	c, _ := k.Open("app", 1)
//	vt := c.VersionChange()
//	vt.Wait() // 等待版本变更事务开始（可能被既有连接阻塞）
//	c.CreateObjectStore("users")
//	vt.Commit() // 版本变更必须显式提交/中止
//
//	// 普通事务：声明模式与非空作用域，回调风格为原生用法。
//	tx, _ := c.Transaction(idb.ReadWrite, []string{"users"})
//	r, _ := tx.PutEx("users", []byte("u1"), []byte("alice"), false)
//	r.OnSuccess = func(*idb.RequestResult) { /* ... */ }
//	_ = tx.Wait() // 请求全部完成后自动提交
//
// 同步风格见 WithTx / SyncGet / SyncPut / SyncDelete。
// 调度规则、可见性与错误次序的完整描述见仓库根目录 DESIGN.md。
package idb

import (
	"sync"
	"time"
)

// Logger 打印输入、输出与判定依据；nil 表示静默。
type Logger interface {
	Printf(format string, args ...any)
}

type Option func(*Kernel)

// Kernel 是事务作用域调度内核。所有数据库状态只在 mu 保护下变更，
// 多连接并发调用因此等价于某个串行顺序。
type Kernel struct {
	mu       sync.Mutex
	dbs      map[string]*database
	log      Logger
	wakeCh   chan struct{}
	deliver  chan *delivery
	stopCh   chan struct{}
	wg       sync.WaitGroup
	paused   bool
	pauseAck chan struct{}
}

// blocker 记录一个阻塞中的版本变更或删除请求（每个数据库至多一个）。
type blocker struct {
	kind    blockerKind
	version int

	upgradeConn *Connection // blkUpgrade：承载版本变更事务的连接
	upgradeTx   *Transaction

	// 通知发出后到达的打开请求，一律排在该版本变更之后。
	queuedOpens []*pendingOpen

	delWaiters []chan struct{}
}

type blockerKind int

const (
	blkUpgrade blockerKind = iota
	blkDelete
)

type pendingOpen struct {
	version int
	wait    chan openResult
}

type openResult struct {
	conn *Connection
	err  error
}

func New(opts ...Option) *Kernel {
	k := &Kernel{
		dbs:      make(map[string]*database),
		wakeCh:   make(chan struct{}, 1),
		deliver:  make(chan *delivery, 4096),
		stopCh:   make(chan struct{}),
		pauseAck: make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(k)
	}
	k.wg.Add(2)
	go k.serveWake()
	go k.serveDeliver()
	return k
}

// Close 停止内核后台循环。
func (k *Kernel) Close() {
	close(k.stopCh)
	k.wg.Wait()
}

func WithLogger(l Logger) Option { return func(k *Kernel) { k.log = l } }

func (k *Kernel) debugf(format string, args ...any) {
	if k.log != nil {
		k.log.Printf(format, args...)
	}
}

// signal 在任何可能改变调度/提交条件的状态变化后调用，唤醒推进循环。
// 永不阻塞：wakeCh 容量为 1，已有待处理信号即代表“稍后再扫描”，信号不会丢。
func (k *Kernel) signal() {
	select {
	case k.wakeCh <- struct{}{}:
	default:
	}
}

// kick 是持锁调用点的便捷封装。
func (k *Kernel) kick() { k.signal() }

// serveWake 是唯一的推进循环：放行就绪事务、执行运行事务的队首请求、
// 检测自动提交。任一时刻在运行的事务两两满足并行规则。
func (k *Kernel) serveWake() {
	defer k.wg.Done()
	for {
		select {
		case <-k.stopCh:
			return
		case <-k.wakeCh:
		}
		k.mu.Lock()
		paused := k.paused
		var ack chan struct{}
		if paused {
			ack = k.pauseAck
		}
		k.mu.Unlock()
		if paused {
			ack <- struct{}{} // 已确认暂停：不会执行本轮 advance
			select {
			case <-k.stopCh:
				return
			case <-time.After(2 * time.Millisecond):
			}
			select {
			case k.wakeCh <- struct{}{}:
			default:
			}
			continue
		}
		k.advance()
	}
}

// Pause 暂停内核推进（调度与请求执行），用于受控地复现调度时序。
// Resume 恢复。仅供测试与确定性演练使用。
func (k *Kernel) Pause() {
	k.mu.Lock()
	k.paused = true
	k.mu.Unlock()
	k.signal()
	<-k.pauseAck // 与 serveWake 握手：它已确认暂停
}

func (k *Kernel) Resume() {
	k.mu.Lock()
	k.paused = false
	k.mu.Unlock()
	k.signal()
}

func (k *Kernel) advance() {
	k.mu.Lock()
	defer k.mu.Unlock()

	// 同一轮内反复扫描：放行事务可能立即让更多事务满足准入条件。
	for {
		progress := false
		for _, d := range k.dbs {
			before := d.sched.runningCount()
			// 阻塞中的版本变更事务由 blocker 解除流程放行，不参与普通调度。
			if d.blocker == nil {
				d.sched.startReady(func(t *Transaction) { t.startLocked() })
			}
			if d.sched.runningCount() != before {
				progress = true
			}
			for _, t := range d.sched.order {
				if t.state == Running && len(t.queue) > 0 {
					ev := t.runOne()
					select {
					case k.deliver <- ev:
						progress = true
					default:
						// 投递通道暂时已满：把请求放回队首，下一轮 wake 重试。
						// advance 因此绝不持锁阻塞，避免与投递循环形成等待环。
						t.queue = append([]*queuedReq{{req: ev.req}}, t.queue...)
					}
				}
				if t.state == Running && len(t.queue) == 0 && t.pending == 0 &&
					t.mode != VersionChange && t.holds == 0 {
					// 没有未完成请求且无显式 hold：自动提交（与显式提交等价）。
					t.commitLocked()
					progress = true
				}
			}
			k.checkBlockerProgress(d)
		}
		if !progress {
			return
		}
	}
}

// serveDeliver 串行投递完成事件；回调在锁外执行，可安全重入公开 API。
func (k *Kernel) serveDeliver() {
	defer k.wg.Done()
	for {
		select {
		case <-k.stopCh:
			return
		case ev := <-k.deliver:
			k.deliverOne(ev)
		}
	}
}

func (k *Kernel) deliverOne(ev *delivery) {
	t := ev.tx
	if !ev.terminal {
		if ev.err != nil {
			k.debugf("tx#%d req store=%q failed: %v (ignorable=%v)",
				t.id, ev.req.store, ev.err, ev.req.Ignorable)
			ev.req.lastErr = ev.err
			if ev.req.OnError != nil {
				ev.req.OnError(ev.err)
			}
			if !ev.req.Ignorable {
				k.mu.Lock()
				if t.state == Running {
					t.abortLocked(ev.err)
				}
				k.mu.Unlock()
			}
		} else {
			k.debugf("tx#%d req store=%q succeeded", t.id, ev.req.store)
			if ev.req.OnSuccess != nil {
				ev.req.OnSuccess(ev.res)
			}
		}
		k.mu.Lock()
		t.pending--
		k.mu.Unlock()
		k.kick()
		return
	}

	if ev.complete {
		k.debugf("tx#%d complete", t.id)
		if t.OnComplete != nil {
			t.OnComplete()
		}
	} else {
		k.debugf("tx#%d abort: %v", t.id, ev.err)
		if t.OnAbort != nil {
			t.OnAbort(ev.err)
		}
	}
	k.mu.Lock()
	k.checkFinishedClose(t.conn)
	k.releaseQueuedOpensLocked(t)
	k.mu.Unlock()
	k.kick()
}

// releaseQueuedOpensLocked 处理版本变更事务结束后排队的打开请求：
// 它们按到达序，在版本变更（无论提交或中止）之后被重新判定。
func (k *Kernel) releaseQueuedOpensLocked(t *Transaction) {
	if t.mode != VersionChange || len(t.queuedOpens) == 0 {
		return
	}
	opens := t.queuedOpens
	t.queuedOpens = nil
	name := t.db.name
	type rr struct {
		p *pendingOpen
		r openResult
	}
	out := make([]rr, len(opens))
	for i, p := range opens {
		out[i] = rr{p: p, r: k.resolveOpenLocked(name, p.version)}
	}
	for _, x := range out {
		x.p.wait <- x.r
	}
}

func (k *Kernel) enqueueDelivery(ev *delivery) {
	if ev != nil {
		k.deliver <- ev
	}
}

// checkFinishedClose 在持锁状态下判断关闭中的连接能否真正关闭。
func (k *Kernel) checkFinishedClose(c *Connection) {
	if c == nil || c.closed || !c.closing {
		return
	}
	for t := range c.txns {
		if t.state == Running || t.state == Queued {
			return
		}
	}
	c.closed = true
	delete(c.db.connections, c)
	close(c.closeDone)
	k.debugf("conn(v=%d) closed; %d connection(s) remain",
		c.version, len(c.db.connections))
	k.checkBlockerProgress(c.db)
}

func (k *Kernel) notifyConnections(d *database) {
	var pending []func()
	for c := range d.connections {
		k.debugf("notifying conn(v=%d) of pending version change/delete", c.version)
		if c.OnVersionChange != nil {
			cb := c.OnVersionChange
			pending = append(pending, cb)
		}
	}
	// 回调可能调用 Close/Open 等公开方法（重新取锁），故在锁外派发。
	go func() {
		for _, cb := range pending {
			cb()
		}
	}()
}

// otherOpenConns 统计除指定升级连接外仍打开的连接。
func (k *Kernel) otherOpenConns(d *database, except *Connection) bool {
	for c := range d.connections {
		if c != except && !c.closed {
			return true
		}
	}
	return false
}

// checkBlockerProgress 在持锁状态下推进阻塞中的版本变更/删除。
func (k *Kernel) checkBlockerProgress(d *database) {
	b := d.blocker
	if b == nil {
		return
	}
	if k.otherOpenConns(d, b.upgradeConn) {
		return
	}

	switch b.kind {
	case blkDelete:
		k.debugf("database %q delete effective; version now 0", d.name)
		d.blocker = nil
		delete(k.dbs, d.name)
		for _, w := range b.delWaiters {
			close(w)
		}
	case blkUpgrade:
		k.debugf("database %q all blockers gone; versionchange tx#%d can run",
			d.name, b.upgradeTx.id)
		b.upgradeTx.queuedOpens = b.queuedOpens
		d.blocker = nil
		k.signal() // blocker 解除：让升级事务参与下一轮调度
	}
}

// Open 打开命名数据库；version=0 表示不指定版本（按当前版本打开）。
func (k *Kernel) Open(name string, version int) (*Connection, error) {
	if name == "" {
		return nil, newError(KindInvalidArgument, "database name must be non-empty")
	}
	if version < 0 {
		return nil, newError(KindInvalidArgument,
			"version must be a non-negative integer (0 = unspecified)")
	}
	k.mu.Lock()

	// 版本变更通知发出后，新的打开请求一律排在该版本变更之后。
	if d, ok := k.dbs[name]; ok && d.blocker != nil && d.blocker.kind == blkUpgrade {
		p := &pendingOpen{version: version, wait: make(chan openResult, 1)}
		d.blocker.queuedOpens = append(d.blocker.queuedOpens, p)
		k.debugf("open(%q, v=%d) queued behind version change", name, version)
		k.mu.Unlock()
		r := <-p.wait
		return r.conn, r.err
	}
	r := k.resolveOpenLocked(name, version)
	k.mu.Unlock()
	return r.conn, r.err
}

// resolveOpenLocked 在持锁状态下执行打开的主体判定，永不阻塞：
// 版本变更即使被既有连接阻塞，也立即返回升级连接（其上事务处于 Queued）。
func (k *Kernel) resolveOpenLocked(name string, version int) openResult {
	d, exists := k.dbs[name]
	if !exists {
		d = newDatabase(name)
		d.k = k
		k.dbs[name] = d
	}

	// 三种版本关系：低于当前拒绝；等于当前（或不指定）直接打开；
	// 高于当前触发版本变更。
	if version != 0 && version < d.version {
		k.debugf("open(%q, v=%d) rejected: version too low (current=%d)",
			name, version, d.version)
		return openResult{err: newError(KindVersionTooLow,
			"requested version %d is lower than current version %d", version, d.version)}
	}
	if version == 0 || version == d.version {
		c := k.newConnectionLocked(d, d.version, false)
		k.debugf("open(%q, v=%d) direct at version %d", name, version, d.version)
		return openResult{conn: c}
	}

	// version > d.version：创建升级连接与版本变更事务（要求独占）。
	c := k.newConnectionLocked(d, version, true)
	t := newTransaction(d, c, VersionChange, map[string]struct{}{})
	t.requestedVersion = version
	c.txns[t] = struct{}{}
	d.sched.register(t)

	if k.otherOpenConns(d, c) {
		// 有其他连接：发通知并进入阻塞态；事务保持 Queued，
		// 待连接全部关闭后才开始。
		d.blocker = &blocker{
			kind: blkUpgrade, version: version,
			upgradeConn: c, upgradeTx: t,
		}
		k.debugf("open(%q, v=%d) upgrade blocked by %d connection(s); notifying",
			name, version, len(d.connections)-1)
		k.notifyConnections(d)
		k.kick()
		return openResult{conn: c}
	}

	k.debugf("open(%q, v=%d) versionchange starts immediately", name, version)
	k.kick()
	return openResult{conn: c}
}

func (k *Kernel) newConnectionLocked(d *database, version int, upgrade bool) *Connection {
	c := &Connection{
		db: d, version: version, upgrade: upgrade,
		txns:      make(map[*Transaction]struct{}),
		closeDone: make(chan struct{}),
	}
	d.connections[c] = struct{}{}
	return c
}

// DeleteDatabase 删除数据库；有打开连接时通知并阻塞，
// 所有连接关闭后删除生效，此后版本视为零。
func (k *Kernel) DeleteDatabase(name string) error {
	if name == "" {
		return newError(KindInvalidArgument, "database name must be non-empty")
	}
	k.mu.Lock()
	d, exists := k.dbs[name]
	if !exists {
		k.debugf("delete(%q): absent, treated as version 0", name)
		k.mu.Unlock()
		return nil
	}
	if d.blocker != nil && d.blocker.kind == blkDelete {
		w := make(chan struct{})
		d.blocker.delWaiters = append(d.blocker.delWaiters, w)
		k.mu.Unlock()
		<-w
		return nil
	}
	if len(d.connections) == 0 {
		k.debugf("delete(%q) immediate; version now 0", name)
		delete(k.dbs, name)
		k.mu.Unlock()
		return nil
	}
	d.blocker = &blocker{kind: blkDelete}
	k.debugf("delete(%q) blocked by %d connection(s); notifying",
		name, len(d.connections))
	k.notifyConnections(d)
	w := make(chan struct{})
	d.blocker.delWaiters = append(d.blocker.delWaiters, w)
	k.mu.Unlock()
	<-w
	return nil
}

// Version 返回数据库当前版本；不存在（已删除/从未创建）时为 0。
func (k *Kernel) Version(name string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if d, ok := k.dbs[name]; ok {
		return d.version
	}
	return 0
}
