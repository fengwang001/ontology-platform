package logstore

import (
	"io"
	"log"
	"sync"
)

// Options 配置日志存储。段大小固定，段总数有上限。
type Options struct {
	SegmentSize uint64
	MaxSegments int
	// Logger 用于打印每次操作的输入、输出与判定依据；nil 时丢弃日志。
	Logger io.Writer
}

// Store 是日志结构存储：块只追加到当前段，段满封存，
// 清理器按代价收益挑选已封存段搬迁存活块并整段回收。
type Store struct {
	mu sync.RWMutex

	segmentSize uint64
	maxSegments int
	logger      *log.Logger

	// 逻辑时钟：每次 Put/Delete 加 1，搬迁不推进时钟。
	clock uint64

	activeID int // 当前追加段；-1 表示尚无段

	segments map[int]*segment
	// index 保存每个键所有仍物理存在的块位置，按写入时刻严格递增。
	index map[string][]loc

	// live[id] 是段 id 的存活字节：索引指向该段的最新值块字节
	// 加上该段中仍需保留的存活墓碑字节。
	live map[int]uint64

	freeIDs []int // 已回收、可重新分配的段号
	nextID  int   // 从未使用过的下一个段号
}

// New 创建一个空的日志存储。
func New(opts Options) *Store {
	if opts.SegmentSize == 0 || opts.MaxSegments <= 0 {
		panic("logstore: SegmentSize must be > 0 and MaxSegments must be > 0")
	}
	var logger *log.Logger
	if opts.Logger != nil {
		logger = log.New(opts.Logger, "[logstore] ", log.LstdFlags|log.Lmicroseconds)
	}
	return &Store{
		segmentSize: opts.SegmentSize,
		maxSegments: opts.MaxSegments,
		logger:      logger,
		activeID:    -1,
		segments:    make(map[int]*segment),
		index:       make(map[string][]loc),
		live:        make(map[int]uint64),
	}
}

// Put 追加写入一个值块；覆盖使旧块失效。
func (s *Store) Put(key string, value []byte) (err error) {
	s.logf("in  Put key=%q valueLen=%d", key, len(value))
	defer func() { s.logf("out Put key=%q err=%v", key, err) }()

	size, err := s.validateBlock(key, value, kindValue)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureRoom(size); err != nil {
		return err
	}

	s.clock++
	ts := s.clock
	seg := s.segments[s.activeID]
	b := &block{key: key, value: append([]byte(nil), value...), kind: kindValue, ts: ts}
	offset := seg.append(b, size)

	locs := s.index[key]
	if len(locs) > 0 {
		last := locs[len(locs)-1]
		if last.kind == kindValue {
			// 旧的最新值块被覆盖，立即失效。
			s.live[last.segID] -= last.size
		}
	}
	locs = append(locs, loc{segID: seg.id, offset: offset, size: size, ts: ts, kind: kindValue})
	s.index[key] = locs
	s.live[seg.id] += size

	s.logf("decision Put key=%q -> seg=%d offset=%d size=%d ts=%d live[%d]=%d",
		key, seg.id, offset, size, ts, seg.id, s.live[seg.id])
	return nil
}

// Delete 追加写入一个墓碑块；键不存在时返回 ErrKeyNotFound。
func (s *Store) Delete(key string) (err error) {
	s.logf("in  Delete key=%q", key)
	defer func() { s.logf("out Delete key=%q err=%v", key, err) }()

	size, err := s.validateBlock(key, nil, kindTomb)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	locs := s.index[key]
	if len(locs) == 0 || locs[len(locs)-1].kind == kindTomb {
		// 键不存在，或最新块已经是墓碑（逻辑上已删除）。
		return ErrKeyNotFound
	}

	if err := s.ensureRoom(size); err != nil {
		return err
	}

	s.clock++
	ts := s.clock
	seg := s.segments[s.activeID]
	b := &block{key: key, value: nil, kind: kindTomb, ts: ts}
	offset := seg.append(b, size)

	// 墓碑使当前最新值块失效。
	last := locs[len(locs)-1]
	s.live[last.segID] -= last.size

	// 墓碑存活条件：任一其他段中仍留有该键更旧的块。
	alive := false
	for _, l := range locs {
		if l.ts < ts && l.segID != seg.id {
			alive = true
			break
		}
	}
	locs = append(locs, loc{segID: seg.id, offset: offset, size: size, ts: ts, kind: kindTomb})
	s.index[key] = locs
	if alive {
		s.live[seg.id] += size
	}

	s.logf("decision Delete key=%q -> seg=%d offset=%d size=%d ts=%d tombAlive=%t",
		key, seg.id, offset, size, ts, alive)
	if alive {
		s.logf("decision tombstone-account ts=%d seg=%d alive=true reason=older-block-in-other-segment",
			ts, seg.id)
	}
	return nil
}

// Get 返回键的最新值；键不存在（含被删除）返回 ErrKeyNotFound。
func (s *Store) Get(key string) (value []byte, err error) {
	s.logf("in  Get key=%q", key)
	defer func() { s.logf("out Get key=%q valueLen=%d err=%v", key, len(value), err) }()

	if key == "" {
		return nil, ErrEmptyKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	locs := s.index[key]
	if len(locs) == 0 {
		return nil, ErrKeyNotFound
	}
	last := locs[len(locs)-1]
	if last.kind == kindTomb {
		return nil, ErrKeyNotFound
	}
	seg := s.segments[last.segID]
	// 通过块本体取最新值，并拷贝一份，避免调用方破坏段内数据。
	var b *block
	for _, candidate := range seg.blocks {
		if candidate.ts == last.ts && candidate.key == key {
			b = candidate
			break
		}
	}
	if b == nil {
		// 账目不变式保证不可达：索引必然指向物理存在的块。
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), b.value...), nil
}

// CleanOnce 按代价收益清理一个已封存段；无可清理段时返回 false。
func (s *Store) CleanOnce() (cleaned bool, err error) {
	s.logf("in  CleanOnce")
	defer func() { s.logf("out CleanOnce cleaned=%t err=%v", cleaned, err) }()

	s.mu.Lock()
	defer s.mu.Unlock()

	victim, basis := s.pickVictim()
	if victim == nil {
		s.logf("decision CleanOnce no sealed segment")
		return false, nil
	}
	if err := s.migrateAndReclaim(victim, basis, true); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) validateBlock(key string, value []byte, kd kind) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	size := blockSize(key, value, kd)
	if size > s.segmentSize {
		return 0, ErrValueTooLarge
	}
	return size, nil
}

// ensureRoom 保证当前段能容纳 size 字节；空间不足时按代价收益先清理。
// 调用方持有写锁。
func (s *Store) ensureRoom(size uint64) error {
	if s.activeID >= 0 && s.segments[s.activeID].free(s.segmentSize) >= size {
		return nil
	}

	// 当前段放不下：有空闲段就直接封存当前段并启用新段。
	if s.freeSegmentCount() > 0 {
		s.rollActive()
		return nil
	}

	// 没有空闲段：尝试清理。只有当存活块能全部搬入当前段剩余空间、
	// 从而净增一个空闲段时才清理；否则整体拒绝（且不追加任何块）。
	victim, basis := s.pickVictim()
	if victim == nil {
		s.logf("decision ensureRoom space-exhausted reason=no-victim size=%d", size)
		return ErrSpaceExhausted
	}
	available := uint64(0)
	if s.activeID >= 0 {
		available = s.segments[s.activeID].free(s.segmentSize)
	}
	if s.live[victim.id] > available {
		s.logf("decision ensureRoom space-exhausted victim=%d live=%d activeFree=%d basis=%s",
			victim.id, s.live[victim.id], available, basis)
		return ErrSpaceExhausted
	}
	if err := s.migrateAndReclaim(victim, basis, false); err != nil {
		return err
	}

	// 回收段已成为空闲段：当前段放不下就启用它。
	if s.segments[s.activeID].free(s.segmentSize) < size {
		s.rollActive()
	}
	return nil
}

func (s *Store) rollActive() {
	if s.activeID >= 0 {
		s.segments[s.activeID].sealed = true
		s.logf("decision seal seg=%d used=%d", s.activeID, s.segments[s.activeID].used)
	}
	id := s.allocID()
	s.segments[id] = &segment{id: id}
	s.live[id] = 0
	s.activeID = id
	s.logf("decision open seg=%d", id)
}

func (s *Store) allocID() int {
	if len(s.freeIDs) > 0 {
		// 回收段号升序复用，保证布局确定。
		id := s.freeIDs[0]
		s.freeIDs = s.freeIDs[1:]
		return id
	}
	id := s.nextID
	s.nextID++
	return id
}

func (s *Store) freeSegmentCount() int {
	return s.maxSegments - len(s.segments)
}

func (s *Store) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}
