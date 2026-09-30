package lsm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	maxKeySize   = 1 << 16
	maxValueSize = 1 << 20
	manifestName = "MANIFEST"
)

// Options 配置存储行为。
type Options struct {
	// MemtableCapacity 内存缓存容纳的记录条数，达到后冻结成段。
	MemtableCapacity int
	// Fanout 扇出阈值：某层段数达到该值时，把最旧的若干段合并进上一层。
	Fanout int
	// MaxLevels 最大层数（层编号 0..MaxLevels-1）。
	MaxLevels int
}

func (o Options) validate() error {
	if o.MemtableCapacity < 1 {
		return fmt.Errorf("%w: MemtableCapacity 必须 >= 1，得到 %d", ErrInvalidArgument, o.MemtableCapacity)
	}
	if o.Fanout < 2 {
		return fmt.Errorf("%w: Fanout 必须 >= 2，得到 %d", ErrInvalidArgument, o.Fanout)
	}
	if o.MaxLevels < 1 {
		return fmt.Errorf("%w: MaxLevels 必须 >= 1，得到 %d", ErrInvalidArgument, o.MaxLevels)
	}
	return nil
}

// memtable 是内存写缓存。记录按追加顺序保存在 order 中，
// index 保存每个键的最新一条，供读取。
type memtable struct {
	mu     sync.RWMutex
	index  map[string]Record
	order  []Record
	writes int
}

func newMemtable() *memtable {
	return &memtable{index: make(map[string]Record)}
}

func (m *memtable) put(rec Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.index[string(rec.Key)] = rec
	m.order = append(m.order, rec)
	m.writes++
}

func (m *memtable) get(key string) (Record, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.index[key]
	return rec, ok
}

// snapshot 是某一时刻完整的只读视图：缓存 + 各层段。
// 通过 atomic.Pointer 整体切换，读者要么看到合并前、要么看到合并后，
// 绝不会看到混合两代的中间态。levels[i][0] 是该层最旧的段。
type snapshot struct {
	mem    *memtable
	levels [][]*Segment
}

// manifest 记录当前生效的段集合，是合并落盘的提交点。
type manifest struct {
	Segments []manifestSegment `json:"segments"`
}

type manifestSegment struct {
	Level int    `json:"level"`
	ID    uint64 `json:"id"`
	File  string `json:"file"`
}

// Store 是分层合并的键值存储。
type Store struct {
	dir  string
	opts Options

	// writeMu 串行化所有写入、冻结与合并。
	writeMu sync.Mutex
	// seq 是全局单调递增的记录序号（写路径下分配）。
	seq uint64
	// segID 是单调递增的段编号。
	segID uint64

	snap   atomic.Pointer[snapshot]
	closed atomic.Bool
}

// Open 打开（或创建）dir 下的存储。加载任何损坏段都会整体失败，
// 不会留下半初始化的状态。
func Open(dir string, opts Options) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: 目录为空", ErrInvalidArgument)
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	entries, err := manifestSegments(dir)
	if err != nil {
		return nil, err
	}
	levels := make([][]*Segment, opts.MaxLevels)
	var maxSeq, maxID uint64
	for _, e := range entries {
		if e.Level < 0 || e.Level >= opts.MaxLevels {
			return nil, fmt.Errorf("%w: 段 %s 层号 %d 超出范围", ErrCorruptSegment, e.File, e.Level)
		}
		seg, err := loadSegment(filepath.Join(dir, e.File), e.ID, e.Level)
		if err != nil {
			return nil, err
		}
		levels[e.Level] = append(levels[e.Level], seg)
		if seg.MaxSeq > maxSeq {
			maxSeq = seg.MaxSeq
		}
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	for l := range levels {
		sort.Slice(levels[l], func(i, j int) bool { return levels[l][i].ID < levels[l][j].ID })
	}
	s := &Store{dir: dir, opts: opts, seq: maxSeq, segID: maxID}
	s.snap.Store(&snapshot{mem: newMemtable(), levels: levels})
	return s, nil
}

// manifestSegments 读取清单；清单不存在时扫描目录中的段文件。
func manifestSegments(dir string) ([]manifestSegment, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if errors.Is(err, os.ErrNotExist) {
		return scanSegments(dir)
	}
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: 清单损坏: %v", ErrCorruptSegment, err)
	}
	return m.Segments, nil
}

func scanSegments(dir string) ([]manifestSegment, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []manifestSegment
	for _, f := range files {
		var level int
		var id uint64
		if n, err := fmt.Sscanf(f.Name(), "seg-L%d-%d.kvs", &level, &id); n == 2 && err == nil {
			out = append(out, manifestSegment{Level: level, ID: id, File: f.Name()})
		}
	}
	return out, nil
}

// writeManifest 原子写入清单（先写临时文件再重命名）。
func writeManifest(dir string, levels [][]*Segment) error {
	var m manifest
	for _, segs := range levels {
		for _, seg := range segs {
			m.Segments = append(m.Segments, manifestSegment{
				Level: seg.Level, ID: seg.ID, File: filepath.Base(seg.path),
			})
		}
	}
	data, err := json.Marshal(&m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, manifestName+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, manifestName))
}

// Put 写入一个键值对。
func (s *Store) Put(key, value []byte) error {
	if err := validateKeyValue(key, value); err != nil {
		return err
	}
	return s.write(Record{Key: key, Value: value})
}

// Delete 写入墓碑。
func (s *Store) Delete(key []byte) error {
	if err := validateKeyValue(key, nil); err != nil {
		return err
	}
	return s.write(Record{Key: key, Tombstone: true})
}

func validateKeyValue(key, value []byte) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if len(key) > maxKeySize {
		return fmt.Errorf("%w: 键长度 %d 超过上限 %d", ErrInvalidArgument, len(key), maxKeySize)
	}
	if len(value) > maxValueSize {
		return fmt.Errorf("%w: 值长度 %d 超过上限 %d", ErrInvalidArgument, len(value), maxValueSize)
	}
	return nil
}

// write 追加一条记录；缓存满则冻结并触发层合并。
// 任何失败都不会改变缓存、段与层分布。
func (s *Store) write(rec Record) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return ErrClosed
	}
	s.seq++
	rec.Seq = s.seq
	snap := s.snap.Load()
	snap.mem.put(rec)
	if snap.mem.writes < s.opts.MemtableCapacity {
		return nil
	}
	return s.freezeLocked()
}

// freezeLocked 把当前缓存冻结成第 0 层段，并逐层做级联合并。
// 调用方需持有 writeMu。只有全部新段落盘且清单提交后才切换快照；
// 中途失败会清理临时段文件并保持原状态不变。
func (s *Store) freezeLocked() error {
	snap := s.snap.Load()
	old := snap.mem

	levels := make([][]*Segment, len(snap.levels))
	for i := range snap.levels {
		levels[i] = append([]*Segment(nil), snap.levels[i]...)
	}

	var created []*Segment
	var retired []*Segment
	cleanup := func(err error) error {
		for _, seg := range created {
			seg.removeFile()
		}
		return err
	}

	s.segID++
	frozen, err := buildSegment(s.dir, s.segID, 0, old.order)
	if err != nil {
		return cleanup(err)
	}
	created = append(created, frozen)
	levels[0] = append(levels[0], frozen)

	for l := 0; l < s.opts.MaxLevels; l++ {
		for len(levels[l]) >= s.opts.Fanout {
			inputs := levels[l][:s.opts.Fanout]
			// 只有在最大层自合并时，输入段已包含全系统最早的写入，
			// 此时才可丢弃墓碑；其余情况墓碑必须保留以遮蔽更旧的段。
			dropTombstones := l == s.opts.MaxLevels-1
			merged := mergeRecords(inputs, dropTombstones)
			levels[l] = levels[l][s.opts.Fanout:]
			retired = append(retired, inputs...)
			if len(merged) == 0 {
				continue
			}
			outLevel := l + 1
			if dropTombstones {
				outLevel = l
			}
			s.segID++
			seg, err := buildSegment(s.dir, s.segID, outLevel, merged)
			if err != nil {
				return cleanup(err)
			}
			created = append(created, seg)
			if dropTombstones {
				// 合并结果比本层剩余段都旧，放在最前保持按时间有序。
				levels[l] = append([]*Segment{seg}, levels[l]...)
			} else {
				levels[outLevel] = append(levels[outLevel], seg)
			}
		}
	}

	if err := writeManifest(s.dir, levels); err != nil {
		return cleanup(err)
	}
	s.snap.Store(&snapshot{mem: newMemtable(), levels: levels})
	for _, seg := range retired {
		seg.removeFile()
	}
	return nil
}

// mergeRecords 按“同键取序号最大的一条”合并多个段；
// dropTombstones 为真时丢弃最终仍为墓碑的记录。
func mergeRecords(inputs []*Segment, dropTombstones bool) []Record {
	latest := make(map[string]Record)
	for _, seg := range inputs {
		for _, rec := range seg.Records {
			key := string(rec.Key)
			if old, ok := latest[key]; !ok || old.Seq < rec.Seq {
				latest[key] = rec
			}
		}
	}
	out := make([]Record, 0, len(latest))
	for _, rec := range latest {
		if dropTombstones && rec.Tombstone {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// Get 读取键的最新可见值。found 为 false 表示不存在或已被删除。
func (s *Store) Get(key []byte) (value []byte, found bool, err error) {
	if len(key) == 0 {
		return nil, false, ErrEmptyKey
	}
	if s.closed.Load() {
		return nil, false, ErrClosed
	}
	snap := s.snap.Load()
	if rec, ok := snap.mem.get(string(key)); ok {
		return resolve(rec)
	}
	for l := 0; l < len(snap.levels); l++ {
		segs := snap.levels[l]
		for i := len(segs) - 1; i >= 0; i-- {
			if rec, ok := segs[i].Get(string(key)); ok {
				return resolve(rec)
			}
		}
	}
	return nil, false, nil
}

func resolve(rec Record) ([]byte, bool, error) {
	if rec.Tombstone {
		return nil, false, nil
	}
	return rec.Value, true, nil
}

// Check 自检：重新解码所有段并校验层级不变量，可并发调用。
func (s *Store) Check() error {
	snap := s.snap.Load()
	seen := make(map[uint64]bool)
	for l, segs := range snap.levels {
		if l < s.opts.MaxLevels-1 && len(segs) >= s.opts.Fanout {
			return fmt.Errorf("层 %d 段数 %d 达到扇出阈值 %d（未触发合并）", l, len(segs), s.opts.Fanout)
		}
		var prevMax uint64
		for i, seg := range segs {
			if seg.ID == 0 || seen[seg.ID] {
				return fmt.Errorf("段编号 %d 重复或非法", seg.ID)
			}
			seen[seg.ID] = true
			if seg.Level != l {
				return fmt.Errorf("段 %d 层号不一致: 记录为 %d，实际位于 %d", seg.ID, seg.Level, l)
			}
			if i > 0 && seg.MaxSeq <= prevMax {
				return fmt.Errorf("层 %d 段 %d 序号范围与更旧的段重叠", l, seg.ID)
			}
			prevMax = seg.MaxSeq
			if err := seg.decodeAll(); err != nil {
				if errors.Is(err, os.ErrNotExist) && !s.segmentLive(seg.ID) {
					// 自检快照取到后，该段被并发合并退役删除，属正常竞态。
					continue
				}
				return err
			}
		}
	}
	return nil
}

// segmentLive 报告段编号是否仍存在于当前快照中。
func (s *Store) segmentLive(id uint64) bool {
	for _, segs := range s.snap.Load().levels {
		for _, seg := range segs {
			if seg.ID == id {
				return true
			}
		}
	}
	return false
}

// Close 把剩余缓存冻结成段并关闭存储。
func (s *Store) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed.Load() {
		return nil
	}
	if s.snap.Load().mem.writes > 0 {
		if err := s.freezeLocked(); err != nil {
			return err
		}
	}
	s.closed.Store(true)
	return nil
}

// levelSizes 返回各层段数，供测试与日志使用。
func (s *Store) levelSizes() []int {
	snap := s.snap.Load()
	sizes := make([]int, len(snap.levels))
	for i, segs := range snap.levels {
		sizes[i] = len(segs)
	}
	return sizes
}

// findTombstone 检查某个键的最新记录是否为墓碑，供测试验证墓碑保留。
func (s *Store) findTombstone(key string) (level int, segID uint64, ok bool) {
	snap := s.snap.Load()
	for l, segs := range snap.levels {
		for i := len(segs) - 1; i >= 0; i-- {
			if rec, hit := segs[i].Get(key); hit && rec.Tombstone {
				return l, segs[i].ID, true
			}
		}
	}
	return 0, 0, false
}
