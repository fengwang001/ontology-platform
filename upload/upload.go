// Package upload 实现分片上传会话的登记与完成校验。
package upload

import (
	"fmt"
	"sync"
)

// Reason 区分操作被拒绝的原因。
type Reason int

const (
	// ReasonInvalidArgument 创建参数非正或会话 ID 冲突。
	ReasonInvalidArgument Reason = iota
	// ReasonNotFound 会话不存在。
	ReasonNotFound
	// ReasonCompleted 会话已完成（含重复完成），已冻结。
	ReasonCompleted
	// ReasonIndexOutOfRange 分片编号越界（不在 1..N）。
	ReasonIndexOutOfRange
	// ReasonInvalidSize 分片大小不合规。
	ReasonInvalidSize
	// ReasonMissingChunks 完成时存在缺片。
	ReasonMissingChunks
)

func (r Reason) String() string {
	switch r {
	case ReasonInvalidArgument:
		return "invalid_argument"
	case ReasonNotFound:
		return "not_found"
	case ReasonCompleted:
		return "completed"
	case ReasonIndexOutOfRange:
		return "index_out_of_range"
	case ReasonInvalidSize:
		return "invalid_size"
	case ReasonMissingChunks:
		return "missing_chunks"
	}
	return "unknown"
}

// Error 是可区分原因的拒绝错误。
type Error struct {
	Reason  Reason
	Session string
	Index   int
	Size    int64
	Missing []int
	Msg     string
}

func (e *Error) Error() string {
	return fmt.Sprintf("upload: %s (session=%q): %s", e.Reason, e.Session, e.Msg)
}

// Stats 是会话统计的快照。
type Stats struct {
	Total      int   // 声明的总片数 N
	MaxSize    int64 // 单片上限
	Uploads    int   // 成功上传次数（含覆盖）
	Chunks     int   // 已登记的不同编号数
	Overwrites int   // 覆盖次数，恒等于 Uploads - Chunks
	Bytes      int64 // 当前各片大小之和
	Completed  bool  // 是否已完成（冻结）
}

type session struct {
	total      int
	maxSize    int64
	chunks     map[int]int64
	uploads    int
	overwrites int
	bytes      int64
	completed  bool
}

// Registry 管理全部分片上传会话，方法均可并发调用。
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*session
}

// NewRegistry 创建空的会话注册表。
func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]*session)}
}

// Create 创建会话：声明总片数 total 与单片上限 maxSize。
// total 或 maxSize 非正、或会话 ID 已存在时整体拒绝。
func (r *Registry) Create(id string, total int, maxSize int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if total <= 0 || maxSize <= 0 {
		return &Error{Reason: ReasonInvalidArgument, Session: id,
			Msg: fmt.Sprintf("total=%d maxSize=%d 必须为正", total, maxSize)}
	}
	if _, ok := r.sessions[id]; ok {
		return &Error{Reason: ReasonInvalidArgument, Session: id,
			Msg: "会话 ID 已存在"}
	}
	r.sessions[id] = &session{total: total, maxSize: maxSize, chunks: make(map[int]int64)}
	return nil
}

// Upload 登记编号为 index（1..N）、大小为 size 的分片；同号再传为覆盖。
// 拒绝优先级固定为：不存在 > 已完成 > 越界 > 大小；被拒绝时不改变任何统计。
func (r *Registry) Upload(id string, index int, size int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return &Error{Reason: ReasonNotFound, Session: id, Index: index, Size: size,
			Msg: "会话不存在"}
	}
	if s.completed {
		return &Error{Reason: ReasonCompleted, Session: id, Index: index, Size: size,
			Msg: "会话已完成并冻结"}
	}
	if index < 1 || index > s.total {
		return &Error{Reason: ReasonIndexOutOfRange, Session: id, Index: index, Size: size,
			Msg: fmt.Sprintf("编号 %d 不在 1..%d", index, s.total)}
	}
	if err := s.checkSize(index, size); err != nil {
		return err
	}
	if old, ok := s.chunks[index]; ok {
		s.bytes += size - old
		s.overwrites++
	} else {
		s.bytes += size
	}
	s.chunks[index] = size
	s.uploads++
	return nil
}

func (s *session) checkSize(index int, size int64) error {
	if size < 1 || size > s.maxSize {
		return &Error{Reason: ReasonInvalidSize, Index: index, Size: size,
			Msg: fmt.Sprintf("大小 %d 不在 1..%d", size, s.maxSize)}
	}
	if index < s.total && size != s.maxSize {
		return &Error{Reason: ReasonInvalidSize, Index: index, Size: size,
			Msg: fmt.Sprintf("非末片大小 %d 必须等于上限 %d", size, s.maxSize)}
	}
	return nil
}

// Complete 校验齐全并完成会话：有缺片时按编号升序一次列全并拒绝，
// 已登记分片保留可补传；成功后会话冻结，重复完成按已完成拒绝。
func (r *Registry) Complete(id string) ([]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return nil, &Error{Reason: ReasonNotFound, Session: id, Msg: "会话不存在"}
	}
	if s.completed {
		return nil, &Error{Reason: ReasonCompleted, Session: id, Msg: "会话已完成（重复完成）"}
	}
	var missing []int
	for i := 1; i <= s.total; i++ {
		if _, ok := s.chunks[i]; !ok {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		return missing, &Error{Reason: ReasonMissingChunks, Session: id, Missing: missing,
			Msg: fmt.Sprintf("缺少 %d 片: %v", len(missing), missing)}
	}
	s.completed = true
	return nil, nil
}

// Stats 返回会话统计快照；会话不存在时拒绝。完成后仍如实返回。
func (r *Registry) Stats(id string) (Stats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return Stats{}, &Error{Reason: ReasonNotFound, Session: id, Msg: "会话不存在"}
	}
	return Stats{
		Total:      s.total,
		MaxSize:    s.maxSize,
		Uploads:    s.uploads,
		Chunks:     len(s.chunks),
		Overwrites: s.overwrites,
		Bytes:      s.bytes,
		Completed:  s.completed,
	}, nil
}
