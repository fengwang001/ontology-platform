// Package sink 按版本幂等应用变更，并保存超 schema 版本行的死信集合。
package sink

import (
	"errors"
	"sync"
)

// ErrDowngrade 表示 SetSchema 试图降低支持的 schema 版本。
var ErrDowngrade = errors.New("sink: schema version downgrade")

// ErrInvalidParam 表示 ns 越界。
var ErrInvalidParam = errors.New("sink: invalid parameter")

// ErrForbidden 表示 SetSchema 的 role 不等于 2。
var ErrForbidden = errors.New("sink: set schema forbidden for role")

// MaxSchema 是 S 的上界（1..100）。
const MaxSchema = int64(100)

// Row 是一次变更行（与 pull.Row 同构，sink 不依赖 pull 以免循环引用）。
type Row struct {
	ID  int64
	Ts  int64
	Ver int64
	Sv  int64
}

// Key 是死信键 (id,ver)。
type Key struct {
	ID  int64
	Ver int64
}

// Outcome 是对一整批行的应用结果。
type Outcome struct {
	Applied int
	Dup     int
	NewDLQ  int
}

// Sink 保存 applied[id]、死信与当前 schema 上限 S。
type Sink struct {
	mu      sync.RWMutex
	s       int64
	applied map[int64]int64
	dlq     map[Key]Row
}

// New 创建 S=s0 的空 sink。
func New(s0 int64) *Sink {
	return &Sink{
		s:       s0,
		applied: make(map[int64]int64),
		dlq:     make(map[Key]Row),
	}
}

// Schema 返回当前支持的最高 schema 版本。
func (s *Sink) Schema() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.s
}

// SetSchema 在 role==2 且 ns 不低于当前 S 时升级；它只改 S，不重放死信。
// 拒绝次序：参数非法（ns 越界）→ 权限不足 → 降级。
func (s *Sink) SetSchema(role, ns int64) error {
	if ns < 1 || ns > MaxSchema {
		return ErrInvalidParam
	}
	if role != 2 {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ns < s.s {
		return ErrDowngrade
	}
	s.s = ns
	return nil
}

// Apply 按规格逐行应用一批行：
// sv>S 时按 (id,ver) 入死信（保留首次进入的行，再次出现不计 NewDLQ）；
// 否则 ver>applied[id] 才应用并推进版本，其余计 Dup。
func (s *Sink) Apply(rows []Row) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out Outcome
	for _, r := range rows {
		if r.Sv > s.s {
			k := Key{ID: r.ID, Ver: r.Ver}
			if _, ok := s.dlq[k]; !ok {
				s.dlq[k] = r
				out.NewDLQ++
			}
			continue
		}
		if r.Ver > s.applied[r.ID] {
			s.applied[r.ID] = r.Ver
			out.Applied++
		} else {
			out.Dup++
		}
	}
	return out
}

// Applied 返回 id 已应用的最大版本（未应用为 0）。
func (s *Sink) Applied(id int64) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.applied[id]
}

// DLQ 返回死信中某键首次进入的行。
func (s *Sink) DLQ(k Key) (Row, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.dlq[k]
	return r, ok
}

// DLQSize 返回死信条目数。
func (s *Sink) DLQSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.dlq)
}
