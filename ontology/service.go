package ontology

import (
	"crypto/rand"
	"fmt"
	"sync"
)

// ErrorKind 区分四类互斥错误，判定次序固定如下。
type ErrorKind int

const (
	ErrStartObjectNotFound ErrorKind = iota // 起始对象不存在（仅首次请求）
	ErrInvalidCursor                        // 游标无法解析或不属于任何已知遍历快照
	ErrInvalidBatchSize                     // 批次大小上限非正整数
	ErrModeChange                           // 首次请求之后尝试更改运行模式
)

// Error 是服务返回的分类错误。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// PageRequest 是一次分页请求。
type PageRequest struct {
	Cursor    string        // 空串表示首次请求
	Spec      TraversalSpec // 仅首次请求使用
	BatchSize int           // 本页批次大小上限，须为正整数
	Mode      Mode          // 首次请求确定后不可更改
}

// PageResponse 是一页结果。
type PageResponse struct {
	Objects []ObjectID
	Markers []TruncationMarker // 仅 ModeMarked 下可能非空
	Cursor  string             // 下一页游标；遍历结束时为空串
	Done    bool               // 遍历是否已在快照上耗尽
}

// RequestLog 记录每次分页请求的处理结果，供审计与测试核对。
type RequestLog struct {
	TraversalID   string
	Snapshot      uint64 // 本次遍历钉住的快照版本
	Cursor        string
	Mode          Mode
	BatchSize     int
	Returned      []ObjectID
	Markers       []TruncationMarker
	HistoryProbes int // 本请求触碰的“已返回判定”历史记录条数
	Err           *Error
}

// Logger 接收每次分页请求的日志。
type Logger interface {
	LogRequest(entry RequestLog)
}

// Service 是分页图遍历服务。所有分页请求在服务内部串行化，
// 因此并发请求的最终效果等价于某个全局顺序下的依次执行。
type Service struct {
	store  *Store
	key    []byte
	logger Logger

	mu       sync.Mutex
	sessions map[string]*session
	nextID   uint64
}

type session struct {
	id      string
	snap    uint64
	mode    Mode
	it      *iterator
	nextSeq int
	release func()
}

// NewService 创建遍历服务。logger 可为 nil（丢弃日志）。
func NewService(store *Store, logger Logger) *Service {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &Service{
		store:    store,
		key:      key,
		logger:   logger,
		sessions: make(map[string]*session),
	}
}

// Page 处理一次分页请求。错误判定次序固定：
// 起始对象不存在（仅首次）→ 游标非法 → 批次大小非正 → 模式变更。
func (s *Service) Page(req PageRequest) (PageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := RequestLog{Cursor: req.Cursor, Mode: req.Mode, BatchSize: req.BatchSize}
	defer func() {
		if s.logger != nil {
			s.logger.LogRequest(entry)
		}
	}()

	fail := func(kind ErrorKind, format string, args ...any) (PageResponse, error) {
		entry.Err = &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
		return PageResponse{}, entry.Err
	}

	var sess *session
	if req.Cursor == "" {
		// 首次请求：钉住遍历开始时的快照。
		snap := s.store.Snapshot()
		if _, ok := s.store.objectAt(snap, req.Spec.Start); !ok {
			return fail(ErrStartObjectNotFound, "start object %q not found", req.Spec.Start)
		}
		if req.BatchSize <= 0 {
			return fail(ErrInvalidBatchSize, "batch size must be a positive integer, got %d", req.BatchSize)
		}
		s.nextID++
		sess = &session{
			id:      fmt.Sprintf("t%06d", s.nextID),
			snap:    snap,
			mode:    req.Mode,
			release: s.store.Pin(snap),
		}
		sess.it = newIterator(s.store, snap, req.Spec, req.Mode)
		s.sessions[sess.id] = sess
	} else {
		tid, seq, err := decodeCursor(s.key, req.Cursor)
		if err != nil {
			return fail(ErrInvalidCursor, "cursor cannot be parsed: %v", err)
		}
		known, ok := s.sessions[tid]
		if !ok {
			return fail(ErrInvalidCursor, "cursor belongs to no known traversal snapshot")
		}
		if seq != known.nextSeq {
			return fail(ErrInvalidCursor,
				"cursor points at already-returned content (seq %d, expect %d)", seq, known.nextSeq)
		}
		if req.BatchSize <= 0 {
			return fail(ErrInvalidBatchSize, "batch size must be a positive integer, got %d", req.BatchSize)
		}
		if req.Mode != known.mode {
			return fail(ErrModeChange, "mode is fixed at first request and cannot change mid-traversal")
		}
		sess = known
	}

	probesBefore := sess.it.historyProbes
	objects := make([]ObjectID, 0, req.BatchSize)
	for len(objects) < req.BatchSize {
		id, ok := sess.it.next()
		if !ok {
			break
		}
		objects = append(objects, id)
	}
	more := sess.it.hasMore()
	markers := sess.it.takeMarkers()
	sess.nextSeq++

	resp := PageResponse{Objects: objects, Markers: markers}
	if more {
		resp.Cursor = encodeCursor(s.key, sess.id, sess.nextSeq)
	} else {
		resp.Done = true
		sess.release()
		delete(s.sessions, sess.id)
	}

	entry.Returned = objects
	entry.Markers = markers
	entry.HistoryProbes = sess.it.historyProbes - probesBefore
	entry.TraversalID = sess.id
	entry.Snapshot = sess.snap
	return resp, nil
}
