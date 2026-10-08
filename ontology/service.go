package ontology

import (
	"fmt"
	"sync"
)

// Service 是本体链接图最短路径服务。所有请求在内部被串行化，
// 串行顺序与日志序号顺序一致。
//
// 并发模型：一把互斥锁同时保护状态与日志追加。变更操作在锁内完成
// 「校验 -> 写日志 -> 应用状态」，查询在锁内读取状态快照，因此任意并发
// 请求的结果都等价于按日志序号顺序的某个串行执行。
type Service struct {
	mu      sync.Mutex
	logPath string
	writer  *logWriter
	st      *state
	nextSeq uint64
	ready   bool
}

// New 创建服务实例，但尚未开始重放；此时一切请求返回 ErrNotReady。
func New(logPath string) *Service {
	return &Service{logPath: logPath, st: newState(), nextSeq: 1}
}

// Replay 仅凭持久化日志从空状态重放；完成后服务就绪。
// 重放期间到达的请求被拒绝并返回 ErrNotReady。
// 若末尾存在撕裂的不完整记录，整体丢弃并把日志截断到有效边界。
func (s *Service) Replay() error {
	entries, validBytes, err := readLog(s.logPath)
	if err != nil {
		return err
	}
	st := newState()
	for _, e := range entries {
		// 重放只回放被接受的操作，直接应用，不做拒绝判定。
		st.apply(e.Op)
	}
	writer, err := openLogWriter(s.logPath)
	if err != nil {
		return err
	}
	if err := writer.truncate(validBytes); err != nil {
		writer.close()
		return err
	}
	s.mu.Lock()
	s.st = st
	s.nextSeq = uint64(len(entries)) + 1
	s.writer = writer
	s.ready = true
	s.mu.Unlock()
	return nil
}

// Apply 校验并执行一次变更操作；被接受则记入日志并返回序号。
// 被拒绝的操作不进入日志，也不改变状态。
func (s *Service) Apply(op Operation, caller UserID) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return 0, ErrNotReady
	}
	if r := s.st.validate(op, caller); r != nil {
		return 0, r
	}
	entry := &LogEntry{Seq: s.nextSeq, Op: op}
	if err := s.writer.append(entry); err != nil {
		return 0, fmt.Errorf("persist log entry: %w", err)
	}
	s.st.apply(op)
	s.nextSeq++
	return entry.Seq, nil
}

// ShortestPath 查询最短路径，不产生日志项。
// 返回的 Metrics 仅供内部验证，不应暴露给调用者。
func (s *Service) ShortestPath(start, end ObjectID, caller UserID) (Path, *Metrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return Path{}, nil, ErrNotReady
	}
	metrics := &Metrics{}
	return s.st.shortestPath(start, end, caller, metrics), metrics, nil
}

// Close 关闭日志文件。
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = false
	if s.writer != nil {
		return s.writer.close()
	}
	return nil
}
