// Package sink 按版本幂等应用行，并维护以 (id,ver) 为键的死信集合。
package sink

// Row 是源端返回的一行：id、语句时间戳 ts、版本 ver、schema 版本 sv。
type Row struct {
	ID  int64
	TS  int64
	Ver int64
	SV  int64
}

// Key 是死信键 (id,ver)。
type Key struct {
	ID  int64
	Ver int64
}

// Outcome 是单行应用结果。
type Outcome int

const (
	Applied Outcome = iota // ver > applied[id]，已应用
	Dup                    // ver <= applied[id]，重复
	DeadNew                // sv > S，首次进入死信
	DeadDup                // sv > S，已在死信中，忽略
)

// Sink 保存 applied 版本表与死信集合。零值可用，但不是并发安全的，
// 并发控制由调用方（pull 包）负责。
type Sink struct {
	applied map[int64]int64
	dead    map[Key]Row
}

// New 返回空 Sink。
func New() *Sink {
	return &Sink{
		applied: make(map[int64]int64),
		dead:    make(map[Key]Row),
	}
}

// Apply 按规则处理一行：
//   - sv > S：若 (id,ver) 不在死信则入死信（保留首次行，不动 applied），否则忽略；
//   - 否则 ver > applied[id] 则应用并置 applied[id]=ver，否则计为 Dup。
func (s *Sink) Apply(r Row, S int64) Outcome {
	if r.SV > S {
		k := Key{ID: r.ID, Ver: r.Ver}
		if _, ok := s.dead[k]; ok {
			return DeadDup
		}
		s.dead[k] = r
		return DeadNew
	}
	if r.Ver > s.applied[r.ID] {
		s.applied[r.ID] = r.Ver
		return Applied
	}
	return Dup
}

// Applied 返回 id 已应用的版本（未应用过为 0）。
func (s *Sink) Applied(id int64) int64 { return s.applied[id] }

// AppliedMap 返回 applied 表的副本。
func (s *Sink) AppliedMap() map[int64]int64 {
	m := make(map[int64]int64, len(s.applied))
	for k, v := range s.applied {
		m[k] = v
	}
	return m
}

// Dead 报告 (id,ver) 是否在死信中。
func (s *Sink) Dead(id, ver int64) bool {
	_, ok := s.dead[Key{ID: id, Ver: ver}]
	return ok
}

// DeadMap 返回死信集合的副本（键为 (id,ver)，值为首次进入的行）。
func (s *Sink) DeadMap() map[Key]Row {
	m := make(map[Key]Row, len(s.dead))
	for k, v := range s.dead {
		m[k] = v
	}
	return m
}
