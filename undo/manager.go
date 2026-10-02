// Package undo 实现带回滚段槽位、段缓存复用与按读视图水位清理的 undo 日志段管理器。
//
// 每个事务最多拥有插入 undo（I）与更新 undo（U）两个段。段占一个槽位，
// 页数为 max(1, ceil(n/K))。提交时 I 段立即按 Release 规则处理，U 段挂入
// 历史链；读视图限值决定历史链中哪些段可以被 Purge 回收。
package undo

import (
	"fmt"
	"sync"
)

const (
	maxSlots       = 1000
	maxPerPage     = 1000
	maxPageBudget  = 1_000_000
	maxTransaction = 1_000_000
)

// Code 区分操作被拒绝的原因。
type Code int

const (
	CodeInvalidParam Code = iota // 参数非法
	CodeTxNotFound               // 事务不存在
	CodeTxExists                 // 事务已存在（含已终止）
	CodeTxTerminated             // 事务已终止
	CodeNoFreeSlot               // 无空闲槽位
	CodePageBudget               // 页预算不足
	CodeViewNotFound             // 视图不存在或已关闭
)

func (c Code) String() string {
	switch c {
	case CodeInvalidParam:
		return "invalid parameter"
	case CodeTxNotFound:
		return "transaction not found"
	case CodeTxExists:
		return "transaction already exists"
	case CodeTxTerminated:
		return "transaction terminated"
	case CodeNoFreeSlot:
		return "no free slot"
	case CodePageBudget:
		return "page budget exhausted"
	case CodeViewNotFound:
		return "view not found"
	}
	return "unknown"
}

// Error 是管理器返回的错误类型，Code 字段给出唯一拒绝原因。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

type segKind int

const (
	kindInsert segKind = iota
	kindUpdate
)

// segment 是一个 undo 段。n 为记录数，pages 为占用页数，
// trxNo 仅在段进入历史链后有效。
type segment struct {
	kind  segKind
	n     int
	pages int
	trxNo int
}

type transaction struct {
	terminated bool
	insert     *segment
	update     *segment
}

// Manager 是并发安全的 undo 日志段管理器。
type Manager struct {
	mu sync.Mutex

	slots      int
	perPage    int
	pageBudget int

	usedSlots int
	usedPages int
	issued    int // 已发出的 trx_no 数
	nextView  int

	txs     map[int]*transaction
	views   map[int]int // 视图号 -> limit
	cacheI  []*segment  // 栈，尾部为栈顶
	cacheU  []*segment
	history []*segment // 头部为最老，trx_no 严格递增
}

// NewManager 构造管理器。slots∈[1,1000]，perPage∈[1,1000]，pageBudget∈[1,1e6]。
func NewManager(slots, perPage, pageBudget int) (*Manager, error) {
	if slots < 1 || slots > maxSlots {
		return nil, newError(CodeInvalidParam, "undo: slots %d out of range [1,%d]", slots, maxSlots)
	}
	if perPage < 1 || perPage > maxPerPage {
		return nil, newError(CodeInvalidParam, "undo: records per page %d out of range [1,%d]", perPage, maxPerPage)
	}
	if pageBudget < 1 || pageBudget > maxPageBudget {
		return nil, newError(CodeInvalidParam, "undo: page budget %d out of range [1,%d]", pageBudget, maxPageBudget)
	}
	return &Manager{
		slots:      slots,
		perPage:    perPage,
		pageBudget: pageBudget,
		txs:        make(map[int]*transaction),
		views:      make(map[int]int),
	}, nil
}

// Snapshot 是管理器内部状态的可观察快照，用于校验与重放对比。
type Snapshot struct {
	UsedSlots int
	UsedPages int
	CacheI    []int // 各缓存段记录数，底->顶
	CacheU    []int
	History   []int // 历史链 trx_no，头->尾
	IssuedTrx int
	OpenViews int
}

// Snapshot 返回当前状态快照。
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() Snapshot {
	snap := Snapshot{
		UsedSlots: m.usedSlots,
		UsedPages: m.usedPages,
		IssuedTrx: m.issued,
		OpenViews: len(m.views),
	}
	for _, s := range m.cacheI {
		snap.CacheI = append(snap.CacheI, s.n)
	}
	for _, s := range m.cacheU {
		snap.CacheU = append(snap.CacheU, s.n)
	}
	for _, s := range m.history {
		snap.History = append(snap.History, s.trxNo)
	}
	return snap
}

// activeTxLocked 按顺序校验：参数非法 -> 事务不存在 -> 事务已终止。
func (m *Manager) activeTxLocked(id int) (*transaction, *Error) {
	if id < 1 || id > maxTransaction {
		return nil, newError(CodeInvalidParam, "undo: transaction id %d out of range [1,%d]", id, maxTransaction)
	}
	tx, ok := m.txs[id]
	if !ok {
		return nil, newError(CodeTxNotFound, "undo: transaction %d not found", id)
	}
	if tx.terminated {
		return nil, newError(CodeTxTerminated, "undo: transaction %d already terminated", id)
	}
	return tx, nil
}

func (m *Manager) cacheFor(kind segKind) *[]*segment {
	if kind == kindInsert {
		return &m.cacheI
	}
	return &m.cacheU
}

// acquireLocked 获取一个该种类的段：优先从缓存栈顶弹出复用
// （不检查槽位与页预算），否则先检查槽位再检查 1 页预算后新建。
func (m *Manager) acquireLocked(kind segKind) (*segment, *Error) {
	cache := m.cacheFor(kind)
	if n := len(*cache); n > 0 {
		top := (*cache)[n-1]
		*cache = (*cache)[:n-1]
		top.n = 0
		return top, nil
	}
	if m.usedSlots >= m.slots {
		return nil, newError(CodeNoFreeSlot, "undo: no free slot (%d/%d used)", m.usedSlots, m.slots)
	}
	if m.usedPages >= m.pageBudget {
		return nil, newError(CodePageBudget, "undo: page budget exhausted (%d/%d used)", m.usedPages, m.pageBudget)
	}
	m.usedSlots++
	m.usedPages++
	return &segment{kind: kind, pages: 1}, nil
}

// releaseLocked 按 Release 规则处理段：只占 1 页且 4n<=3K 时压入该种
// 缓存（保留槽位与页），否则归还槽位与全部页。
func (m *Manager) releaseLocked(seg *segment) {
	if seg.pages == 1 && 4*seg.n <= 3*m.perPage {
		cache := m.cacheFor(seg.kind)
		*cache = append(*cache, seg)
		return
	}
	m.usedSlots--
	m.usedPages -= seg.pages
}

// Begin 登记事务 t。已登记过（含已终止）报事务已存在。
func (m *Manager) Begin(t int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t < 1 || t > maxTransaction {
		return newError(CodeInvalidParam, "undo: transaction id %d out of range [1,%d]", t, maxTransaction)
	}
	if _, ok := m.txs[t]; ok {
		return newError(CodeTxExists, "undo: transaction %d already exists", t)
	}
	m.txs[t] = &transaction{}
	return nil
}

// Insert 向 t 的插入 undo 段追加一条记录。
func (m *Manager) Insert(t int) error {
	return m.appendRecord(t, kindInsert)
}

// Modify 向 t 的更新 undo 段追加一条记录。
func (m *Manager) Modify(t int) error {
	return m.appendRecord(t, kindUpdate)
}

func (m *Manager) appendRecord(t int, kind segKind) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.activeTxLocked(t)
	if err != nil {
		return err
	}
	var seg *segment
	if kind == kindInsert {
		seg = tx.insert
	} else {
		seg = tx.update
	}
	if seg == nil {
		acquired, aerr := m.acquireLocked(kind)
		if aerr != nil {
			return aerr
		}
		seg = acquired
		if kind == kindInsert {
			tx.insert = seg
		} else {
			tx.update = seg
		}
	}
	if seg.n+1 > m.perPage*seg.pages {
		if m.usedPages >= m.pageBudget {
			return newError(CodePageBudget, "undo: page budget exhausted (%d/%d used)", m.usedPages, m.pageBudget)
		}
		seg.pages++
		m.usedPages++
	}
	seg.n++
	return nil
}

// Commit 提交事务：分配 trx_no，I 段立即 Release，U 段挂入历史链尾部。
func (m *Manager) Commit(t int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.activeTxLocked(t)
	if err != nil {
		return err
	}
	m.issued++
	if tx.insert != nil {
		m.releaseLocked(tx.insert)
		tx.insert = nil
	}
	if tx.update != nil {
		tx.update.trxNo = m.issued
		m.history = append(m.history, tx.update)
		tx.update = nil
	}
	tx.terminated = true
	return nil
}

// Rollback 回滚事务：所有段立即 Release，不消耗 trx_no。
func (m *Manager) Rollback(t int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.activeTxLocked(t)
	if err != nil {
		return err
	}
	if tx.insert != nil {
		m.releaseLocked(tx.insert)
		tx.insert = nil
	}
	if tx.update != nil {
		m.releaseLocked(tx.update)
		tx.update = nil
	}
	tx.terminated = true
	return nil
}

// OpenView 打开读视图，返回视图号；其 limit 为调用时已发 trx_no 数加一。
func (m *Manager) OpenView() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextView++
	m.views[m.nextView] = m.issued + 1
	return m.nextView
}

// CloseView 关闭视图；不存在或已关闭报视图不存在。
func (m *Manager) CloseView(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.views[id]; !ok {
		return newError(CodeViewNotFound, "undo: view %d not found", id)
	}
	delete(m.views, id)
	return nil
}

// purgeLimitLocked 计算清理限值 PL：全部打开视图 limit 的最小值，
// 无视图时为已发 trx_no 数加一。
func (m *Manager) purgeLimitLocked() int {
	pl := m.issued + 1
	for _, limit := range m.views {
		if limit < pl {
			pl = limit
		}
	}
	return pl
}

// Purge 从历史链头部起回收 trx_no 严格小于 PL 的段，至多 n 个，
// 返回按次序回收的 trx_no 列表；遇到不满足者即停。
func (m *Manager) Purge(n int) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n < 1 {
		return nil, newError(CodeInvalidParam, "undo: purge count %d < 1", n)
	}
	pl := m.purgeLimitLocked()
	var purged []int
	for len(purged) < n && len(m.history) > 0 && m.history[0].trxNo < pl {
		head := m.history[0]
		m.history = m.history[1:]
		purged = append(purged, head.trxNo)
		m.releaseLocked(head)
	}
	return purged, nil
}
