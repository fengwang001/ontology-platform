// Package cmt 在 ofs 单分区状态之上管理多个 CDC 分区：分区声明、投递登记、
// Ack、提交位点快照，以及丢弃未提交状态的崩溃重启模拟。
// 所有方法均自带互斥锁，可被多 goroutine 并发调用。
package cmt

import (
	"errors"
	"sort"
	"sync"

	"ontology/ofs"
)

// 多分区层哨兵错误，与 ofs 的两个哨兵、彼此之间均互不相同。
var (
	// ErrPartitionNotAssigned：操作的分区尚未经 Assign 声明。
	ErrPartitionNotAssigned = errors.New("cmt: partition not assigned")
	// ErrTooManyInFlight：该分区已投递未提交的位点数将超过上限。
	ErrTooManyInFlight = errors.New("cmt: in-flight offset count exceeds limit")
)

// Manager 管理全部分区。零值不可用，须经 NewManager 构造。
type Manager struct {
	mu          sync.Mutex
	maxInFlight int64 // <= 0 表示不限
	parts       map[int]*ofs.State
}

// NewManager 创建多分区管理器；maxInFlight 为单分区在途位点上限，<=0 不限。
func NewManager(maxInFlight int) *Manager {
	return &Manager{maxInFlight: int64(maxInFlight), parts: map[int]*ofs.State{}}
}

// Assign 声明分区 p，起点 start 等价于该分区初始已提交位点。
func (m *Manager) Assign(p int, start int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.parts[p]; ok {
		return // 已声明，保持既有状态不动。
	}
	m.parts[p] = ofs.New(start)
}

// Assigned 报告分区是否已声明。
func (m *Manager) Assigned(p int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.parts[p]
	return ok
}

func (m *Manager) state(p int) (*ofs.State, error) {
	s, ok := m.parts[p]
	if !ok {
		return nil, ErrPartitionNotAssigned
	}
	return s, nil
}

// Deliver 在分区 p 登记投递位点 off。未声明分区、在途超限、位点不连续
// 均整体失败且不留任何状态改变。
func (m *Manager) Deliver(p int, off int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.state(p)
	if err != nil {
		return err
	}
	if m.maxInFlight > 0 && s.InFlight() >= m.maxInFlight {
		return ErrTooManyInFlight
	}
	return s.Deliver(off)
}

// Ack 在分区 p 登记位点 off 处理完成（乱序、重复均安全）。
// 未声明分区返回 ErrPartitionNotAssigned；越界透传 ofs.ErrAckOutOfRange。
func (m *Manager) Ack(p int, off int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.state(p)
	if err != nil {
		return err
	}
	return s.Ack(off)
}

// Committed 返回分区 p 当前已提交位点；未声明分区返回 ErrPartitionNotAssigned。
func (m *Manager) Committed(p int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.state(p)
	if err != nil {
		return 0, err
	}
	return s.Committed(), nil
}

// Snapshot 是某一时刻全部分区的已提交位点快照。
type Snapshot struct {
	Committed map[int]int64
}

// Commit 返回全部分区可提交位点的快照（键为分区号）。
func (m *Manager) Commit() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int]int64, len(m.parts))
	for p, s := range m.parts {
		out[p] = s.Committed()
	}
	return Snapshot{Committed: out}
}

// Partitions 返回已声明分区号的有序副本。
func (m *Manager) Partitions() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, 0, len(m.parts))
	for p := range m.parts {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// Restart 模拟崩溃重启：丢弃所有未提交的投递与 Ack，每个分区以其当前
// 已提交位点 C 作为新起点重建（重启后从 C 开始重新投递）。
func (m *Manager) Restart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p, s := range m.parts {
		m.parts[p] = ofs.New(s.Committed())
	}
}
