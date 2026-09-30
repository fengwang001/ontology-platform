package deadlock

import (
	"log"
	"sort"
	"sync"
)

// Mode 是锁的共享/排他模式。
type Mode int

const (
	Shared Mode = iota
	Exclusive
)

func (m Mode) String() string {
	if m == Shared {
		return "S"
	}
	return "X"
}

func compatible(a, b Mode) bool { return a == Shared && b == Shared }

type txnState int

const (
	stActive txnState = iota
	stWaiting
	stAborted
	stEnded
)

func (s txnState) String() string {
	switch s {
	case stActive:
		return "active"
	case stWaiting:
		return "waiting"
	case stAborted:
		return "aborted"
	case stEnded:
		return "ended"
	}
	return "?"
}

type lockRef struct {
	site string
	lock string
}

type waitReq struct {
	ref  lockRef
	mode Mode
}

// Txn 是一个事务的本地视图。
type Txn struct {
	id    string
	ts    int64
	state txnState
	held  map[lockRef]Mode
	wait  *waitReq
}

type qReq struct {
	txn  string
	mode Mode
}

type lockEntry struct {
	holders map[string]Mode
	queue   []qReq
}

// Manager 维护全部站点的锁表与等待关系，并处理探测消息。
// 全部导出方法可并发调用。
type Manager struct {
	mu      sync.Mutex
	sites   map[string]map[string]*lockEntry
	txns    map[string]*Txn
	tsIndex map[int64]string
	net     Network
	logger  *log.Logger
	victims []string
}

func NewManager(logger *log.Logger) *Manager {
	return &Manager{
		sites:   make(map[string]map[string]*lockEntry),
		txns:    make(map[string]*Txn),
		tsIndex: make(map[int64]string),
		logger:  logger,
	}
}

// SetNetwork 注入消息网络（延迟/乱序由实现决定）。
func (m *Manager) SetNetwork(n Network) { m.net = n }

// AddSite 注册一个站点。
func (m *Manager) AddSite(site string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sites[site]; !ok {
		m.sites[site] = make(map[string]*lockEntry)
	}
}

// AddLock 在站点上注册一把锁。
func (m *Manager) AddLock(site, lock string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	locks, ok := m.sites[site]
	if !ok {
		return reject(ErrUnknownSite, "site %q", site)
	}
	if _, ok := locks[lock]; !ok {
		locks[lock] = &lockEntry{holders: make(map[string]Mode)}
	}
	return nil
}

// Begin 以唯一开始时间戳开启事务。
func (m *Manager) Begin(txn string, startTS int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.txns[txn]; ok {
		return reject(ErrDuplicateTxn, "txn %q already exists", txn)
	}
	if other, ok := m.tsIndex[startTS]; ok {
		return reject(ErrDuplicateStartTS, "start-ts %d already used by %q", startTS, other)
	}
	m.txns[txn] = &Txn{id: txn, ts: startTS, state: stActive, held: make(map[lockRef]Mode)}
	m.tsIndex[startTS] = txn
	m.logf("op=Begin txn=%s ts=%d result=ok", txn, startTS)
	return nil
}

// Request 请求锁；授予返回 true，排队返回 false，被拒绝返回可区分错误。
func (m *Manager) Request(txn, site, lock string, mode Mode) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, le, err := m.checkRequest(txn, site, lock)
	if err != nil {
		m.logf("op=Request txn=%s site=%s lock=%s mode=%s result=rejected reason=%v", txn, site, lock, mode, err)
		return false, err
	}
	ref := lockRef{site, lock}
	if _, ok := t.held[ref]; ok {
		m.logf("op=Request txn=%s site=%s lock=%s mode=%s result=granted note=already-held", txn, site, lock, mode)
		return true, nil
	}
	if len(le.queue) == 0 && holdersCompatible(le, mode) {
		le.holders[txn] = mode
		t.held[ref] = mode
		m.logf("op=Request txn=%s site=%s lock=%s mode=%s result=granted", txn, site, lock, mode)
		return true, nil
	}
	le.queue = append(le.queue, qReq{txn: txn, mode: mode})
	t.state = stWaiting
	t.wait = &waitReq{ref: ref, mode: mode}
	waits := m.waitsOnLocked(t)
	m.logf("op=Request txn=%s site=%s lock=%s mode=%s result=queued waits=%v", txn, site, lock, mode, waits)
	m.initiateProbeLocked(t, waits)
	return false, nil
}

// Release 释放事务持有的一把锁并按 FCFS 授予等待者。
func (m *Manager) Release(txn, site, lock string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txns[txn]
	if !ok {
		return reject(ErrUnknownTxn, "txn %q", txn)
	}
	le, err := m.lookupLock(site, lock)
	if err != nil {
		return err
	}
	ref := lockRef{site, lock}
	if _, ok := t.held[ref]; !ok {
		return reject(ErrLockNotHeld, "txn %q does not hold %s/%s", txn, site, lock)
	}
	delete(le.holders, txn)
	delete(t.held, ref)
	m.logf("op=Release txn=%s site=%s lock=%s result=ok", txn, site, lock)
	m.grantWaitersLocked(ref, le)
	return nil
}

// End 结束事务：移出等待队列、释放全部锁并按序授予等待者。
func (m *Manager) End(txn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txns[txn]
	if !ok {
		return reject(ErrUnknownTxn, "txn %q", txn)
	}
	if t.state == stEnded || t.state == stAborted {
		return reject(ErrTxnFinished, "txn %q already %s", txn, t.state)
	}
	m.finishTxnLocked(t, stEnded)
	m.logf("op=End txn=%s result=ok", txn)
	return nil
}

// TxnState 返回事务当前状态，供本地验证。
func (m *Manager) TxnState(txn string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.txns[txn]; ok {
		return t.state.String()
	}
	return "unknown"
}

// WaitsFor 返回事务此刻等待的事务集合（有序、确定）。
func (m *Manager) WaitsFor(txn string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.txns[txn]; ok && t.state == stWaiting {
		return m.waitsOnLocked(t)
	}
	return nil
}

// HasCycle 检测当前等待图中是否仍存在等待环，供本地验证。
func (m *Manager) HasCycle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		if t, ok := m.txns[id]; ok && t.state == stWaiting {
			for _, next := range m.waitsOnLocked(t) {
				if color[next] == gray {
					return true
				}
				if color[next] == white && visit(next) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for id := range m.txns {
		if color[id] == white && visit(id) {
			return true
		}
	}
	return false
}

func (m *Manager) logf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Printf(format, args...)
	}
}

func (m *Manager) lookupLock(site, lock string) (*lockEntry, error) {
	locks, ok := m.sites[site]
	if !ok {
		return nil, reject(ErrUnknownSite, "site %q", site)
	}
	le, ok := locks[lock]
	if !ok {
		return nil, reject(ErrUnknownLock, "lock %q on site %q", lock, site)
	}
	return le, nil
}

func (m *Manager) checkRequest(txn, site, lock string) (*Txn, *lockEntry, error) {
	t, ok := m.txns[txn]
	if !ok {
		return nil, nil, reject(ErrUnknownTxn, "txn %q", txn)
	}
	switch t.state {
	case stEnded, stAborted:
		return nil, nil, reject(ErrTxnFinished, "txn %q already %s", txn, t.state)
	case stWaiting:
		return nil, nil, reject(ErrTxnWaiting, "txn %q is waiting on %s/%s", txn, t.wait.ref.site, t.wait.ref.lock)
	}
	le, err := m.lookupLock(site, lock)
	if err != nil {
		return nil, nil, err
	}
	return t, le, nil
}

func holdersCompatible(le *lockEntry, mode Mode) bool {
	for _, hm := range le.holders {
		if !compatible(mode, hm) {
			return false
		}
	}
	return true
}

// waitsOnLocked 由当前锁表现算等待边：全部不相容持有者 +
// 队列中排在前面且不相容的等待者。结果排序保证确定性。
func (m *Manager) waitsOnLocked(t *Txn) []string {
	if t.state != stWaiting || t.wait == nil {
		return nil
	}
	locks, ok := m.sites[t.wait.ref.site]
	if !ok {
		return nil
	}
	le, ok := locks[t.wait.ref.lock]
	if !ok {
		return nil
	}
	set := make(map[string]struct{})
	for holder, hm := range le.holders {
		if holder != t.id && !compatible(t.wait.mode, hm) {
			set[holder] = struct{}{}
		}
	}
	for _, q := range le.queue {
		if q.txn == t.id {
			break
		}
		if !compatible(t.wait.mode, q.mode) {
			set[q.txn] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// grantWaitersLocked 按先来先服务授予：一个等待请求与当前全部持有者
// 及排在其前的全部未授予等待者相容时即可授予。
func (m *Manager) grantWaitersLocked(ref lockRef, le *lockEntry) {
	var remaining []qReq
	for _, q := range le.queue {
		grant := holdersCompatible(le, q.mode)
		if grant {
			for _, ahead := range remaining {
				if !compatible(q.mode, ahead.mode) {
					grant = false
					break
				}
			}
		}
		if !grant {
			remaining = append(remaining, q)
			continue
		}
		le.holders[q.txn] = q.mode
		if t, ok := m.txns[q.txn]; ok && t.wait != nil && t.wait.ref == ref {
			t.state = stActive
			t.held[ref] = q.mode
			t.wait = nil
			m.logf("grant txn=%s site=%s lock=%s mode=%s", q.txn, ref.site, ref.lock, q.mode)
		}
	}
	le.queue = remaining
}

// finishTxnLocked 结束（提交或中止）事务：移出等待队列、释放全部锁、按序授予。
func (m *Manager) finishTxnLocked(t *Txn, final txnState) {
	if t.wait != nil {
		if locks, ok := m.sites[t.wait.ref.site]; ok {
			if le, ok := locks[t.wait.ref.lock]; ok {
				filtered := le.queue[:0]
				for _, q := range le.queue {
					if q.txn != t.id {
						filtered = append(filtered, q)
					}
				}
				le.queue = filtered
			}
		}
		t.wait = nil
	}
	refs := make([]lockRef, 0, len(t.held))
	for ref := range t.held {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].site != refs[j].site {
			return refs[i].site < refs[j].site
		}
		return refs[i].lock < refs[j].lock
	})
	for _, ref := range refs {
		if le, err := m.lookupLock(ref.site, ref.lock); err == nil {
			delete(le.holders, t.id)
		}
		delete(t.held, ref)
	}
	t.state = final
	for _, ref := range refs {
		if le, err := m.lookupLock(ref.site, ref.lock); err == nil {
			m.grantWaitersLocked(ref, le)
		}
	}
}

// Victims 按判定顺序返回被中止的事务序列。
func (m *Manager) Victims() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.victims))
	copy(out, m.victims)
	return out
}
