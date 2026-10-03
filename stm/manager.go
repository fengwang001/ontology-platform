// Package stm 实现软件事务内存（STM）的竞争管理器。
package stm

import (
	"errors"
	"fmt"
	"sync"
)

// Mode 表示事务对对象的持有方式。
type Mode int

const (
	ModeNone  Mode = iota // 无
	ModeRead              // 读
	ModeWrite             // 写
)

// Config 为竞争管理器的构造参数。
type Config struct {
	M int // 对象数，1..64
	L int // 特权阈值，1..16
	D int // 基础延迟，1..1000
	E int // 指数封顶，0..20
	P int // 积分上限，1..1000
	Q int // 强制阈值，1..16
}

// 各类拒绝原因。
var (
	ErrInvalidConfig = errors.New("配置非法")
	ErrNoSuchTxn     = errors.New("事务号不存在")
	ErrBadState      = errors.New("状态不符")
	ErrBadObject     = errors.New("对象越界")
)

type status int

const (
	stActive status = iota
	stAborted
	stCommitted
)

type txn struct {
	kp    int
	ab    int
	att   []int
	holds []Mode
	st    status
}

// Manager 为可并发调用的竞争管理器。
type Manager struct {
	mu   sync.Mutex
	cfg  Config
	txns []*txn
}

// NewManager 校验配置并创建管理器；任一参数越界则整体拒绝。
func NewManager(cfg Config) (*Manager, error) {
	if cfg.M < 1 || cfg.M > 64 ||
		cfg.L < 1 || cfg.L > 16 ||
		cfg.D < 1 || cfg.D > 1000 ||
		cfg.E < 0 || cfg.E > 20 ||
		cfg.P < 1 || cfg.P > 1000 ||
		cfg.Q < 1 || cfg.Q > 16 {
		return nil, fmt.Errorf("%w: %+v", ErrInvalidConfig, cfg)
	}
	return &Manager{cfg: cfg}, nil
}

// OpenResult 为 Open 的结果。
type OpenResult struct {
	Acquired bool  // 是否获得（含已持有不弱于所求方式的无变化情形）
	Aborted  []int // 本次被中止的事务号，升序；未中止他人时为空
	Delay    int   // 未获得时的退避延迟 D*2^min(k,E)
}

// Begin 开启新事务，返回从 1 起递增的事务号。
func (m *Manager) Begin() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &txn{
		att:   make([]int, m.cfg.M),
		holds: make([]Mode, m.cfg.M),
		st:    stActive,
	}
	m.txns = append(m.txns, tx)
	return len(m.txns)
}

func (m *Manager) lookup(t int) (*txn, error) {
	if t < 1 || t > len(m.txns) {
		return nil, fmt.Errorf("%w: %d", ErrNoSuchTxn, t)
	}
	return m.txns[t-1], nil
}

func (m *Manager) delay(exp int) int {
	if exp > m.cfg.E {
		exp = m.cfg.E
	}
	return m.cfg.D * (1 << exp)
}

// grant 让 tx 以 want 方式获得对象 o。
func (m *Manager) grant(tx *txn, o int, want Mode) {
	if tx.holds[o] == ModeNone {
		if tx.kp < m.cfg.P {
			tx.kp++
		}
	}
	tx.holds[o] = want
	tx.att[o] = 0
}

// abort 中止事务 e：ab 加一、kp 上整减半、释放全部持有并清空 att。
func abortTxn(e *txn) {
	e.st = stAborted
	e.ab++
	e.kp = (e.kp + 1) / 2
	for i := range e.holds {
		e.holds[i] = ModeNone
		e.att[i] = 0
	}
}

// privileged 报告事务是否为特权事务（ab 不小于 L）。
func (m *Manager) privileged(tx *txn) bool {
	return tx.ab >= m.cfg.L
}

// dominates 报告 t 以连续失败次数 k 是否压过敌手 e。
func (m *Manager) dominates(t, e *txn, tid, eid, k int) bool {
	tp, ep := m.privileged(t), m.privileged(e)
	switch {
	case tp && ep:
		return tid < eid
	case tp:
		return true
	case ep:
		return false
	default:
		return k >= m.cfg.Q || t.kp+k > e.kp
	}
}

// Open 让事务 t 以读/写方式打开对象 o。
func (m *Manager) Open(t, o int, write bool) (OpenResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.lookup(t)
	if err != nil {
		return OpenResult{}, err
	}
	if tx.st != stActive {
		return OpenResult{}, fmt.Errorf("%w: Open 须活跃: 事务 %d", ErrBadState, t)
	}
	if o < 0 || o >= m.cfg.M {
		return OpenResult{}, fmt.Errorf("%w: %d", ErrBadObject, o)
	}
	want := ModeRead
	if write {
		want = ModeWrite
	}
	if tx.holds[o] >= want {
		return OpenResult{Acquired: true}, nil
	}
	var adversaries []int
	for id, other := range m.txns {
		if id == t-1 {
			continue
		}
		h := other.holds[o]
		if h == ModeNone || (!write && h != ModeWrite) {
			continue
		}
		adversaries = append(adversaries, id+1)
	}
	if len(adversaries) == 0 {
		m.grant(tx, o, want)
		return OpenResult{Acquired: true}, nil
	}
	k := tx.att[o]
	for _, eid := range adversaries {
		if !m.dominates(tx, m.txns[eid-1], t, eid, k) {
			tx.att[o] = k + 1
			return OpenResult{Delay: m.delay(k)}, nil
		}
	}
	for _, eid := range adversaries {
		abortTxn(m.txns[eid-1])
	}
	m.grant(tx, o, want)
	return OpenResult{Acquired: true, Aborted: adversaries}, nil
}

// Restart 令已中止的 t 重新活跃，保留 kp 与 ab，返回退避延迟。
func (m *Manager) Restart(t int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.lookup(t)
	if err != nil {
		return 0, err
	}
	if tx.st != stAborted {
		return 0, fmt.Errorf("%w: Restart 须已中止: 事务 %d", ErrBadState, t)
	}
	tx.st = stActive
	for i := range tx.holds {
		tx.holds[i] = ModeNone
		tx.att[i] = 0
	}
	exp := tx.ab - 1
	if exp < 0 {
		exp = 0
	}
	return m.delay(exp), nil
}

// Commit 释放全部持有并转已提交。
func (m *Manager) Commit(t int) error {
	return m.finish(t, stCommitted, "Commit")
}

// Abort 自愿中止，不改 ab 与 kp。
func (m *Manager) Abort(t int) error {
	return m.finish(t, stAborted, "Abort")
}

func (m *Manager) finish(t int, st status, op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.lookup(t)
	if err != nil {
		return err
	}
	if tx.st != stActive {
		return fmt.Errorf("%w: %s 须活跃: 事务 %d", ErrBadState, op, t)
	}
	tx.st = st
	for i := range tx.holds {
		tx.holds[i] = ModeNone
		tx.att[i] = 0
	}
	return nil
}

// Score 返回事务 t 的积分 kp 与被中止次数 ab。
func (m *Manager) Score(t int) (kp, ab int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.lookup(t)
	if err != nil {
		return 0, 0, err
	}
	return tx.kp, tx.ab, nil
}

// Holding 返回事务 t 对对象 o 的持有方式。
func (m *Manager) Holding(t, o int) (Mode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.lookup(t)
	if err != nil {
		return ModeNone, err
	}
	if o < 0 || o >= m.cfg.M {
		return ModeNone, fmt.Errorf("%w: %d", ErrBadObject, o)
	}
	return tx.holds[o], nil
}
