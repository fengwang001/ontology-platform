// Package resume 记录断点状态（已确认写出的字节位置、块内偏移）并支持恢复。
package resume

import (
	"sync"

	"ontology/chunker"
)

// Checkpoint 是断点快照：配合管线保留的未确认帧序列即可无损续写。
type Checkpoint struct {
	Confirmed int64         // 已确认写出的编码字节数
	Accepted  int64         // 已入队的编码字节数
	Chunks    int64         // 已产生的块数
	FrameOff  int           // 当前帧内已确认偏移（块内偏移）
	Closed    bool          // 结束块是否已入队
	Chunker   chunker.State // 切块器快照
}

// Store 是并发安全的断点存储（进程内存）。
type Store struct {
	mu sync.Mutex
	cp Checkpoint
	ok bool
}

// Save 覆盖保存最新断点。
func (s *Store) Save(cp Checkpoint) {
	s.mu.Lock()
	s.cp, s.ok = cp, true
	s.mu.Unlock()
}

// Load 读取最近保存的断点。
func (s *Store) Load() (Checkpoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cp, s.ok
}
