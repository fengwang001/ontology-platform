package tso

import (
	"context"
	"sync"
)

// 共享存储接受写入的任期条件中，“不小于存量”包含相等的情形：
// 同一主节点续写时任期不变，因此必须放行相等任期。

// BoundRecord 是共享存储中保存的（任期，上界）记录。
// Upper 为已持久化的物理毫秒上界（开区间）：主节点只允许发出
// Physical < Upper 的时间戳。
type BoundRecord struct {
	Term  int64
	Upper int64
}

// Store 抽象共享存储。写入以任期为乐观锁：
// 仅当 wantTerm 不小于存量任期时才原子写入；存量任期更大时
// 返回 ErrTermSuperseded，由调用方降为从节点。
type Store interface {
	Read(ctx context.Context) (BoundRecord, error)
	Write(ctx context.Context, wantTerm int64, rec BoundRecord) error
}

// WriteFault 描述对下一次 Write 调用注入的故障。
type WriteFault int

const (
	WriteFaultNone WriteFault = iota
	WriteFaultFail
	WriteFaultSupersede
)

// MemStore 是带互斥与可选故障注入的内存共享存储。
type MemStore struct {
	mu       sync.Mutex
	rec      BoundRecord
	failNext WriteFault
}

// NewMemStore 创建存量为 (0, 0) 的内存存储。
func NewMemStore() *MemStore { return &MemStore{} }

func (s *MemStore) Read(ctx context.Context) (BoundRecord, error) {
	if err := ctx.Err(); err != nil {
		return BoundRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec, nil
}

func (s *MemStore) Write(ctx context.Context, wantTerm int64, rec BoundRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext != WriteFaultNone {
		fault := s.failNext
		s.failNext = WriteFaultNone
		switch fault {
		case WriteFaultFail:
			return ErrInjectedWriteFailure
		case WriteFaultSupersede:
			// 模拟在写入之前已有更高任期的主节点写入过共享存储。
			s.rec = BoundRecord{Term: wantTerm + 1, Upper: rec.Upper}
			return ErrTermSuperseded
		}
	}
	if wantTerm < s.rec.Term {
		return ErrTermSuperseded
	}
	s.rec = rec
	return nil
}

// InjectFault 让下一次 Write 按 fault 失败（一次性）。
func (s *MemStore) InjectFault(fault WriteFault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = fault
}

// Current 返回存储中当前的（任期，上界），供测试与观测使用。
func (s *MemStore) Current() BoundRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec
}
