package logkv

import (
	"bytes"
	"os"
	"sort"
	"sync"
	"sync/atomic"
)

// Config 是引擎的配置。
type Config struct {
	// Dir 是存储目录，不存在时自动创建。
	Dir string
	// MaxSegmentBytes 是活动段的大小上限，超过后封口并新建活动段。
	// 必须大于 0。
	MaxSegmentBytes int64
	// WriteHints 控制封口（含合并输出封口）时是否生成提示信息。
	WriteHints bool
}

// Status 是读取一个键的三种可区分结果。
type Status int

const (
	// StatusNotFound 键从未出现（或删除标记已被合并清除）。
	StatusNotFound Status = iota
	// StatusFound 键存在。
	StatusFound
	// StatusDeleted 键的最新记录是删除标记。
	StatusDeleted
)

func (s Status) String() string {
	switch s {
	case StatusFound:
		return "found"
	case StatusDeleted:
		return "deleted"
	default:
		return "not-found"
	}
}

// Stats 是引擎运行期可查询的统计。
type Stats struct {
	// TruncatedBytes 是最近一次恢复因撕裂尾部截断的字节数。
	TruncatedBytes uint64
	// Distortions 是读取时发现的目录失真次数（每次都会报告）。
	Distortions uint64
	// SelfHeals 是目录失真自愈成功的次数。
	SelfHeals uint64
	// RecordReads 是读取路径上对盘上记录的读取次数，
	// 用于验证一次读取的开销与总键数、总段数无关。
	RecordReads uint64
	// HintSegments 是最近一次恢复中直接采用提示信息的段数。
	HintSegments int
	// ScannedSegments 是最近一次恢复中逐条扫描重建的段数。
	ScannedSegments int
}

// Store 是只追加日志结构键值引擎。所有公开方法可并发调用，
// 结果等价于某个串行顺序。
type Store struct {
	mu       sync.RWMutex
	cfg      Config
	dir      string
	segments map[uint32]*segment // 已封口段
	active   *segment            // 活动段
	maxID    uint32
	keys     *keydir
	activeKD map[string]keydirEntry // 活动段内每键最新记录，用于生成提示
	nextSeq  uint64
	stats    Stats
	closed   bool
	// recordReads 在读锁下更新，须用原子操作；Stats 时并入快照。
	recordReads atomic.Uint64

	// testHook 在合并的各阶段被调用，仅测试用于注入崩溃。
	testHook func(stage string)
}

// Open 打开或创建存储目录并执行启动恢复。恢复失败时不产生
// 任何部分状态（返回的 Store 为 nil）。
func Open(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, newError(KindInvalidArgument, "empty directory")
	}
	if cfg.MaxSegmentBytes <= 0 {
		return nil, newError(KindInvalidArgument, "MaxSegmentBytes must be positive")
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		cfg:      cfg,
		dir:      cfg.Dir,
		segments: make(map[uint32]*segment),
		keys:     newKeydir(),
		nextSeq:  1,
	}
	if err := s.recover(); err != nil {
		s.closeFiles()
		return nil, err
	}
	return s, nil
}

// Close 关闭全部段文件。关闭后的调用报参数非法。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.closeFiles()
	return nil
}

func (s *Store) closeFiles() {
	for _, seg := range s.segments {
		seg.close()
	}
	if s.active != nil {
		s.active.close()
	}
}

// Stats 返回统计快照。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.stats
	st.RecordReads = s.recordReads.Load()
	return st
}

// NextSeq 返回下一条记录将分配的写序号，供测试与调用方验证
// 写序号在整个存储内单调。
func (s *Store) NextSeq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextSeq
}

// Put 追加一条键值记录。
func (s *Store) Put(key, value []byte) error {
	if len(key) == 0 {
		return newError(KindInvalidArgument, "empty key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return newError(KindInvalidArgument, "store closed")
	}
	return s.appendRecord(key, value, false)
}

// Delete 追加一条删除标记。
func (s *Store) Delete(key []byte) error {
	if len(key) == 0 {
		return newError(KindInvalidArgument, "empty key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return newError(KindInvalidArgument, "store closed")
	}
	return s.appendRecord(key, nil, true)
}

func (s *Store) appendRecord(key, value []byte, tombstone bool) error {
	buf := EncodeRecord(s.nextSeq, key, value, tombstone)
	if s.active.size > 0 && s.active.size+int64(len(buf)) > s.cfg.MaxSegmentBytes {
		if err := s.rotate(); err != nil {
			return err
		}
	}
	if _, err := s.active.file.WriteAt(buf, s.active.size); err != nil {
		return err
	}
	entry := keydirEntry{
		Segment:   s.active.id,
		Offset:    uint64(s.active.size),
		Length:    uint32(len(buf)),
		Seq:       s.nextSeq,
		Tombstone: tombstone,
	}
	s.active.size += int64(len(buf))
	s.keys.apply(key, entry)
	s.activeKD[string(key)] = entry
	s.nextSeq++
	return nil
}

// rotate 封口当前活动段（可选生成提示信息）并新建下一个活动段。
func (s *Store) rotate() error {
	old := s.active
	if s.cfg.WriteHints {
		if err := s.writeHint(old.id, s.activeKD, old.size); err != nil {
			return err
		}
	}
	old.sealed = true
	old.file.Sync()
	s.segments[old.id] = old
	next, err := openSegment(s.dir, s.maxID+1, false)
	if err != nil {
		return err
	}
	s.maxID++
	s.active = next
	s.activeKD = make(map[string]keydirEntry)
	return nil
}

// writeHint 为已封口段生成提示信息。entries 按键排序以保证
// 相同操作序列重放得到完全相同的字节。
func (s *Store) writeHint(segID uint32, entries map[string]keydirEntry, validBytes int64) error {
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	sort.Strings(names)
	h := HintData{ValidBytes: uint64(validBytes)}
	for _, k := range names {
		e := entries[k]
		h.Entries = append(h.Entries, HintEntry{
			Seq:       e.Seq,
			Offset:    e.Offset,
			Length:    e.Length,
			Tombstone: e.Tombstone,
			Key:       []byte(k),
		})
	}
	return os.WriteFile(hintPath(s.dir, segID), EncodeHint(h), 0o644)
}

// getOutcome 是一次目录查找加记录读取的结果。
type getOutcome struct {
	value     []byte
	status    Status
	entry     keydirEntry
	distorted bool
}

// Get 读取一个键，返回存在 / 已删除 / 从未出现三种结果之一。
func (s *Store) Get(key []byte) ([]byte, Status, error) {
	if len(key) == 0 {
		return nil, StatusNotFound, newError(KindInvalidArgument, "empty key")
	}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, StatusNotFound, newError(KindInvalidArgument, "store closed")
	}
	out, err := s.getLocked(key)
	s.mu.RUnlock()
	if err != nil || !out.distorted {
		return out.value, out.status, err
	}
	// 目录失真：升级为写锁，扫描该段重建后重试。
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err = s.getLocked(key)
	if err != nil {
		return nil, StatusNotFound, err
	}
	if !out.distorted {
		// 已被并发调用者自愈。
		return out.value, out.status, nil
	}
	s.stats.Distortions++
	if herr := s.healSegment(out.entry.Segment); herr != nil {
		return nil, StatusNotFound, herr
	}
	out, err = s.getLocked(key)
	if err != nil {
		return nil, StatusNotFound, err
	}
	if out.distorted {
		return nil, StatusNotFound, corruptError(out.entry.Segment, int64(out.entry.Offset),
			"directory still inconsistent after rebuild")
	}
	s.stats.SelfHeals++
	return out.value, out.status, nil
}

// getLocked 完成一次目录查找与记录读取，调用方须持有读锁或写锁。
func (s *Store) getLocked(key []byte) (getOutcome, error) {
	var out getOutcome
	entry, ok := s.keys.lookup(key)
	if !ok {
		out.status = StatusNotFound
		return out, nil
	}
	out.entry = entry
	seg := s.segmentByID(entry.Segment)
	if seg == nil {
		return out, corruptError(entry.Segment, int64(entry.Offset), "directory points to unknown segment")
	}
	rec, err := readRecordAt(seg.file, int64(entry.Offset), entry.Length)
	s.recordReads.Add(1)
	if err != nil {
		return out, corruptError(entry.Segment, int64(entry.Offset), "record unreadable or checksum mismatch")
	}
	if rec.Seq != entry.Seq || !bytes.Equal(rec.Key, key) {
		out.distorted = true
		return out, nil
	}
	if rec.Tombstone {
		out.status = StatusDeleted
		return out, nil
	}
	out.status = StatusFound
	out.value = append([]byte(nil), rec.Value...)
	return out, nil
}

func (s *Store) segmentByID(id uint32) *segment {
	if s.active != nil && s.active.id == id {
		return s.active
	}
	return s.segments[id]
}

// healSegment 扫描指定已封口段并重建该段在键目录中的全部条目。
func (s *Store) healSegment(segID uint32) error {
	seg := s.segments[segID]
	if seg == nil {
		return corruptError(segID, -1, "cannot heal active or unknown segment")
	}
	f, err := os.Open(seg.path)
	if err != nil {
		return err
	}
	defer f.Close()
	res, err := scanSegment(segID, f, seg.size, false)
	if err != nil {
		return err
	}
	s.keys.removeSegment(segID)
	for _, sr := range res.records {
		s.keys.apply(sr.Key, keydirEntry{
			Segment:   segID,
			Offset:    uint64(sr.Offset),
			Length:    sr.Length,
			Seq:       sr.Seq,
			Tombstone: sr.Tombstone,
		})
	}
	return nil
}

// recover 执行启动恢复：清理合并残留、逐段重建键目录。
func (s *Store) recover() error {
	ids, err := s.cleanupMergeArtifacts()
	if err != nil {
		return err
	}
	var maxSeq uint64
	for i, id := range ids {
		sealed := i < len(ids)-1
		seg, err := openSegment(s.dir, id, sealed)
		if err != nil {
			return err
		}
		if sealed {
			s.segments[id] = seg
			seq, err := s.recoverSealedSegment(seg)
			if err != nil {
				return err
			}
			if seq > maxSeq {
				maxSeq = seq
			}
			continue
		}
		// 活动段：允许撕裂尾部并截断。
		s.active = seg
		s.activeKD = make(map[string]keydirEntry)
		res, err := scanSegment(id, seg.file, seg.size, true)
		if err != nil {
			return err
		}
		if res.tornBytes > 0 {
			if err := os.Truncate(seg.path, res.validBytes); err != nil {
				return err
			}
			s.stats.TruncatedBytes += uint64(res.tornBytes)
		}
		seg.size = res.validBytes
		for _, sr := range res.records {
			entry := keydirEntry{
				Segment:   id,
				Offset:    uint64(sr.Offset),
				Length:    sr.Length,
				Seq:       sr.Seq,
				Tombstone: sr.Tombstone,
			}
			s.keys.apply(sr.Key, entry)
			s.activeKD[string(sr.Key)] = entry
			if sr.Seq > maxSeq {
				maxSeq = sr.Seq
			}
		}
		s.maxID = id
	}
	if s.active == nil {
		seg, err := openSegment(s.dir, 1, false)
		if err != nil {
			return err
		}
		s.active = seg
		s.activeKD = make(map[string]keydirEntry)
		s.maxID = 1
	}
	s.nextSeq = maxSeq + 1
	return nil
}

// recoverSealedSegment 恢复一个已封口段：提示信息存在、自带校验
// 通过且声明的有效字节数等于段实际大小（已封口段的全部字节均
// 有效）时直接采用；否则作废提示信息，扫描全段重建。
func (s *Store) recoverSealedSegment(seg *segment) (uint64, error) {
	var maxSeq uint64
	if data, err := os.ReadFile(hintPath(s.dir, seg.id)); err == nil {
		if h, ok := DecodeHint(data); ok && h.ValidBytes == uint64(seg.size) {
			for _, e := range h.Entries {
				s.keys.apply(e.Key, keydirEntry{
					Segment:   seg.id,
					Offset:    e.Offset,
					Length:    e.Length,
					Seq:       e.Seq,
					Tombstone: e.Tombstone,
				})
				if e.Seq > maxSeq {
					maxSeq = e.Seq
				}
			}
			s.stats.HintSegments++
			return maxSeq, nil
		}
		// 提示信息作废，回退到全段扫描；扫描结果为权威。
	}
	res, err := scanSegment(seg.id, seg.file, seg.size, false)
	if err != nil {
		return 0, err
	}
	for _, sr := range res.records {
		s.keys.apply(sr.Key, keydirEntry{
			Segment:   seg.id,
			Offset:    uint64(sr.Offset),
			Length:    sr.Length,
			Seq:       sr.Seq,
			Tombstone: sr.Tombstone,
		})
		if sr.Seq > maxSeq {
			maxSeq = sr.Seq
		}
	}
	s.stats.ScannedSegments++
	return maxSeq, nil
}
