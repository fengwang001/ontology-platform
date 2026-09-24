// Package mrg 按会话键 Sid 管理多个会话，把 Append/Close 分发到 seg，
// 并负责键校验、并发互斥与 O(1) 哈希定位。依赖方向：mrg -> seg。
package mrg

import (
	"errors"
	"sync"

	"ontology/seg"
)

// 可判定哨兵错误：键错误定义在本层，其余三类透传 seg 的同一哨兵，
// 这样上层只需依赖 mrg 即可用 errors.Is/== 判定全部四类故障。
var (
	ErrBadKey     = errors.New("mrg: empty session key")
	ErrInvalid    = seg.ErrInvalid
	ErrConflict   = seg.ErrConflict
	ErrIncomplete = seg.ErrIncomplete
	ErrClosed     = seg.ErrClosed
)

// Event 是上游分区投来的一条事件。
type Event struct {
	Sid   string // 归并键
	Seq   int    // 会话内序号，从 1 起
	Value int    // [0,9] 的一位数字
}

// Mgr 是全部会话的进程内状态。零值不可用，用 New 创建。
type Mgr struct {
	mu sync.Mutex
	// sessions 以 Sid 做哈希定位；Append 只检查命中的这一个会话。
	sessions map[string]*seg.Seg
	// probe 是非导出计数器：最近一次 Append 定位会话时检查过的会话个数。
	// 哈希定位恒为 1；整表扫描会随表长增长。只能由同包测试直接读取。
	probe int
}

// New 创建空管理器。
func New() *Mgr {
	return &Mgr{sessions: make(map[string]*seg.Seg)}
}

// Append 按 Sid 定位会话并转发事件。
// 键非法时不定位、不改任何状态；其余判定（非法序号/冲突/已关闭）由 seg 完成，
// seg 的拒绝发生在写入前，故失败不留痕。
func (m *Mgr) Append(ev Event) error {
	if ev.Sid == "" {
		return ErrBadKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[ev.Sid]
	m.probe = 1 // 哈希定位：只检查命中（或新建）的这一个会话
	if !ok {
		// 先在临时会话上判定，成功才入表——被拒事件不得留下空会话。
		s = seg.New()
		if err := s.Append(ev.Seq, ev.Value); err != nil {
			return err
		}
		m.sessions[ev.Sid] = s
		return nil
	}
	return s.Append(ev.Seq, ev.Value)
}

// Close 按 Sid 定位会话并尝试以 N 关闭。未知会话视为空会话，必然不完整。
func (m *Mgr) Close(sid string, n int) error {
	if sid == "" {
		return ErrBadKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sid]
	if !ok {
		return seg.ErrIncomplete
	}
	return s.Close(n)
}

// Result 返回会话的冻结结果；未知/未关闭会话 ok 为 false。
func (m *Mgr) Result(sid string) (value int, ok bool, err error) {
	if sid == "" {
		return 0, false, ErrBadKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, exists := m.sessions[sid]
	if !exists {
		return 0, false, nil
	}
	v, closed := s.Result()
	return v, closed, nil
}

// Seen 返回某会话已见 Seq 的升序副本；未知会话返回 nil。诊断用。
func (m *Mgr) Seen(sid string) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sid]; ok {
		return s.Seen()
	}
	return nil
}
