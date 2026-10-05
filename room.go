// Package ontology 实现组队语音房的权限与麦序管理器。
//
// Manager 编排 role（角色等级与任免）、mic（麦位与排麦队列）、
// voice（禁言记录）三个包。每个操作的流程为：参数检查 → 时钟检查 →
// 快照 → 入口补麦 → 业务判定 → 执行 → 出口补麦 → 推进时钟；任一判定
// 失败即整体回滚，不留痕迹也不推进时钟。所有操作由一把互斥锁串行化，
// 并发调用等价于某个串行顺序。
package ontology

import (
	"errors"
	"sync"

	"ontology/mic"
	"ontology/role"
	"ontology/voice"
)

// 各类拒绝原因，可用 errors.Is 区分。判定优先级（高在前）：
// 参数非法 > 时钟回退 > 操作者不在房间 > 目标不在房间 > 等级不足 >
// 被压制 > 其余状态类（须先移交、未禁言、禁言中、重复、不在麦序）。
var (
	ErrParam           = errors.New("参数非法")
	ErrClock           = errors.New("时钟回退")
	ErrNotInRoom       = errors.New("操作者不在房间")
	ErrTargetNotInRoom = errors.New("目标不在房间")
	ErrLevel           = errors.New("等级不足")
	ErrSuppressed      = errors.New("禁言被压制")
	ErrMustTransfer    = errors.New("须先移交")
	ErrNotMuted        = errors.New("未禁言")
	ErrMuted           = errors.New("禁言中")
	ErrDuplicate       = errors.New("重复")
	ErrNotInMicOrder   = errors.New("不在麦序")
)

const maxClock = int64(1_000_000_000_000)

// Manager 为组队语音房管理器，可并发调用。
type Manager struct {
	mu     sync.Mutex
	roles  *role.Table
	mics   *mic.State
	voice  *voice.Book
	maxNow int64 // 已接受操作的最大 now
}

// New 创建 M 个麦位的管理器，M 取 1 到 64，麦位编号 0 到 M-1。
func New(m int) *Manager {
	if m < 1 || m > 64 {
		panic("ontology: mic count out of range [1, 64]")
	}
	return &Manager{
		roles: role.NewTable(),
		mics:  mic.New(m),
		voice: voice.NewBook(),
	}
}

type snapshot struct {
	roles *role.Table
	mics  *mic.State
	voice *voice.Book
}

func (m *Manager) snapshot() snapshot {
	return snapshot{m.roles.Clone(), m.mics.Clone(), m.voice.Clone()}
}

func (m *Manager) restore(s snapshot) {
	m.roles, m.mics, m.voice = s.roles, s.mics, s.voice
}

func (m *Manager) fill(now int64) {
	m.mics.Fill(func(u string) bool { return m.voice.Muted(u, now) })
}

// run 串行化执行一个操作：时钟检查 → 快照 → 入口补麦 → 业务判定与执行 →
// 出口补麦 → 推进时钟；业务判定失败则连同入口补麦一并回滚。
func (m *Manager) run(now int64, fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || now > maxClock {
		return ErrParam
	}
	if now < m.maxNow {
		return ErrClock
	}
	snap := m.snapshot()
	m.fill(now)
	if err := fn(); err != nil {
		m.restore(snap)
		return err
	}
	m.fill(now)
	if now > m.maxNow {
		m.maxNow = now
	}
	return nil
}

// Join 加入房间：房间为空时成为 Owner，否则为 Member；已在房间报重复。
func (m *Manager) Join(now int64, u string) error {
	if u == "" {
		return ErrParam
	}
	return m.run(now, func() error {
		if m.roles.Has(u) {
			return ErrDuplicate
		}
		m.roles.Join(u)
		return nil
	})
}

// SetRole 任免角色，r 只能是 Admin 或 Member；要求 by 的等级严格高于
// target 当前等级且严格高于 r 的等级。
func (m *Manager) SetRole(now int64, by, target string, r role.Level) error {
	if r != role.Admin && r != role.Member {
		return ErrParam
	}
	return m.run(now, func() error {
		if !m.roles.Has(by) {
			return ErrNotInRoom
		}
		if !m.roles.Has(target) {
			return ErrTargetNotInRoom
		}
		if lv := m.roles.Level(by); lv <= m.roles.Level(target) || lv <= r {
			return ErrLevel
		}
		m.roles.Set(target, r)
		return nil
	})
}

// Transfer 移交 Owner：by 必须是 Owner，target 获得 Owner，by 降为 Admin。
func (m *Manager) Transfer(now int64, by, target string) error {
	if by == target {
		return ErrParam
	}
	return m.run(now, func() error {
		if !m.roles.Has(by) {
			return ErrNotInRoom
		}
		if !m.roles.Has(target) {
			return ErrTargetNotInRoom
		}
		if m.roles.Level(by) != role.Owner {
			return ErrLevel
		}
		m.roles.Transfer(by, target)
		return nil
	})
}

// Leave 离开房间：Owner 在房间还有他人时须先移交。离开者下麦或出队，
// 其禁言记录保留，重新加入后仍生效。
func (m *Manager) Leave(now int64, u string) error {
	return m.run(now, func() error {
		if !m.roles.Has(u) {
			return ErrNotInRoom
		}
		if m.roles.Level(u) == role.Owner && m.roles.Size() > 1 {
			return ErrMustTransfer
		}
		m.roles.Remove(u)
		m.mics.Drop(u)
		return nil
	})
}

// Mute 禁言 target 直到 until（now < until 生效，取等解除）。要求 by 的
// 等级严格高于 target；若 target 已有生效禁言且其施加等级 L0 高于 by
// 当前等级，报被压制；否则覆盖 until（可缩短），L0 快照为 by 此刻等级。
// 被禁言者若在麦上立即下麦且不入队，若在队列中则保留原位置。
func (m *Manager) Mute(now int64, by, target string, until int64) error {
	if until <= now {
		return ErrParam
	}
	return m.run(now, func() error {
		if !m.roles.Has(by) {
			return ErrNotInRoom
		}
		if !m.roles.Has(target) {
			return ErrTargetNotInRoom
		}
		lv := m.roles.Level(by)
		if lv <= m.roles.Level(target) {
			return ErrLevel
		}
		if rec, ok := m.voice.Get(target); ok && now < rec.Until && rec.L0 > lv {
			return ErrSuppressed
		}
		m.voice.Set(target, until, lv)
		m.mics.ForceDropMic(target)
		return nil
	})
}

// Unmute 解除禁言，等级与压制检查同 Mute；target 无生效禁言报未禁言。
func (m *Manager) Unmute(now int64, by, target string) error {
	return m.run(now, func() error {
		if !m.roles.Has(by) {
			return ErrNotInRoom
		}
		if !m.roles.Has(target) {
			return ErrTargetNotInRoom
		}
		lv := m.roles.Level(by)
		if lv <= m.roles.Level(target) {
			return ErrLevel
		}
		rec, ok := m.voice.Get(target)
		active := ok && now < rec.Until
		if active && rec.L0 > lv {
			return ErrSuppressed
		}
		if !active {
			return ErrNotMuted
		}
		m.voice.Unset(target)
		return nil
	})
}

// TakeMic 上麦：有生效禁言者报禁言中；已在麦上或队列中者报重复；
// 有空麦则上编号最小的空麦，否则排到队尾。
func (m *Manager) TakeMic(now int64, u string) error {
	return m.run(now, func() error {
		if !m.roles.Has(u) {
			return ErrNotInRoom
		}
		if m.voice.Muted(u, now) {
			return ErrMuted
		}
		if m.mics.OnMic(u) || m.mics.InQueue(u) {
			return ErrDuplicate
		}
		m.mics.Take(u)
		return nil
	})
}

// DropMic 下麦或出队；两者都不是报不在麦序。
func (m *Manager) DropMic(now int64, u string) error {
	return m.run(now, func() error {
		if !m.roles.Has(u) {
			return ErrNotInRoom
		}
		if !m.mics.OnMic(u) && !m.mics.InQueue(u) {
			return ErrNotInMicOrder
		}
		m.mics.Drop(u)
		return nil
	})
}

// MuteView 为一条禁言记录的只读视图。
type MuteView struct {
	Until int64
	L0    role.Level
}

// View 为管理器某一时刻的只读状态视图，供测试与回放对照。
type View struct {
	Roles map[string]role.Level
	Mics  []string // "" 表示空麦
	Queue []string // 队首到队尾
	Mutes map[string]MuteView
}

// Snapshot 返回当前状态视图。
func (m *Manager) Snapshot() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := View{
		Roles: m.roles.Roles(),
		Mics:  m.mics.Slots(),
		Queue: m.mics.Queue(),
		Mutes: make(map[string]MuteView),
	}
	for u, r := range m.voice.Records() {
		v.Mutes[u] = MuteView{Until: r.Until, L0: r.L0}
	}
	return v
}
