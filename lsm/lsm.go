package lsm

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Options 控制缓存容量、扇出阈值与最大层级。
type Options struct {
	MemtableSize int // 缓存记录数达到该值即冻结成段进入第零层
	Fanout       int // 某层段数达到该值即触发合并
	MaxLevel     int // 最高层编号（0 起），最大层满时在本层内继续合并
}

// snapshot 是某一时刻的完整不可变状态。读路径只接触快照，
// 因此读永远不会看到“半次合并”的混合两代记录：每次发布都是整棵状态原子替换。
type snapshot struct {
	mem       []record // 追加顺序，读时从尾向头
	levels    [][]*segment
	floor     []*segment // LoadSegment 注入的外部段，全局最旧，读序在最后
	nextSegID uint64
	nextSeq   uint64
}

// DB 是分层合并键值存储。
type DB struct {
	opts    Options
	writeMu sync.Mutex // 串行化所有状态变更（Put/Delete/LoadSegment）
	snap    atomic.Pointer[snapshot]
}

// New 创建空存储。非法参数返回 ErrInvalidArgument。
func New(opts Options) (*DB, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	s := &snapshot{
		levels:    make([][]*segment, opts.MaxLevel+1),
		nextSegID: 1,
		nextSeq:   1,
	}
	d := &DB{opts: opts}
	d.snap.Store(s)
	return d, nil
}

// Put 追加一条普通写入。key 为空返回 ErrEmptyKey，失败不改变任何状态。
func (d *DB) Put(key string, value []byte) error {
	if key == "" {
		return ErrEmptyKey
	}
	if value == nil {
		return fmt.Errorf("%w: nil value for key %q", ErrInvalidArgument, key)
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	c := cloneSnapshot(d.snap.Load())
	c.mem = append(c.mem, record{seq: c.nextSeq, key: key, value: append([]byte(nil), value...)})
	c.nextSeq++

	froze := len(c.mem) >= d.opts.MemtableSize
	settle(c, froze, d.opts)

	d.snap.Store(c) // 单次原子发布：读者要么看到旧完整状态，要么看到新完整状态
	return nil
}

// Delete 追加一条墓碑记录。
func (d *DB) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	c := cloneSnapshot(d.snap.Load())
	c.mem = append(c.mem, record{seq: c.nextSeq, key: key, tombstone: true})
	c.nextSeq++

	settle(c, len(c.mem) >= d.opts.MemtableSize, d.opts)

	d.snap.Store(c)
	return nil
}

// Get 返回最新写入值。墓碑生效（键不存在可见值）时 ok=false。
func (d *DB) Get(key string) (value []byte, ok bool, err error) {
	if key == "" {
		return nil, false, ErrEmptyKey
	}
	v, _, _, ok := searchSnapshot(d.snap.Load(), key)
	return v, ok, nil
}

// GetWithSource 同 Get，并返回命中记录的 seq 与所在层（-1 表示内存缓存）。
func (d *DB) GetWithSource(key string) (value []byte, seq uint64, level int, ok bool, err error) {
	if key == "" {
		return nil, 0, 0, false, ErrEmptyKey
	}
	v, seq, lvl, found := searchSnapshot(d.snap.Load(), key)
	return v, seq, lvl, found, nil
}

// searchSnapshot 在单一快照上按 内存 -> 第0层..最大层 -> floor 的顺序找最新记录。
func searchSnapshot(s *snapshot, key string) (value []byte, seq uint64, level int, ok bool) {
	for i := len(s.mem) - 1; i >= 0; i-- {
		if s.mem[i].key == key {
			r := s.mem[i]
			if r.tombstone {
				return nil, r.seq, -1, false
			}
			return append([]byte(nil), r.value...), r.seq, -1, true
		}
	}
	for li, lvl := range s.levels {
		for i := len(lvl) - 1; i >= 0; i-- { // 层内段号升序，最新在末尾
			if r, found := lookupRecord(lvl[i], key); found {
				if r.tombstone {
					return nil, r.seq, li, false
				}
				return append([]byte(nil), r.value...), r.seq, li, true
			}
		}
	}
	for i := len(s.floor) - 1; i >= 0; i-- {
		if r, found := lookupRecord(s.floor[i], key); found {
			if r.tombstone {
				return nil, r.seq, len(s.levels), false
			}
			return append([]byte(nil), r.value...), r.seq, len(s.levels), true
		}
	}
	return nil, 0, 0, false
}

func lookupRecord(seg *segment, key string) (record, bool) {
	i := sort.Search(len(seg.records), func(i int) bool { return seg.records[i].key >= key })
	if i < len(seg.records) && seg.records[i].key == key {
		return seg.records[i], true
	}
	return record{}, false
}

// LoadSegment 把外部编码的段原子加载为全局最旧段（置于 floor）。
// 段损坏/截断/非法时整体拒绝，缓存、段与层分布保持原样不变。
func (d *DB) LoadSegment(data []byte) error {
	seg, err := decodeSegment(data, true) // 严格模式：不完整即拒绝
	if err != nil {
		return err
	}
	if len(seg.records) == 0 {
		return fmt.Errorf("%w: loaded segment contains no records", ErrInvalidArgument)
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	c := cloneSnapshot(d.snap.Load())
	c.floor = append(c.floor, seg)
	sort.Slice(c.floor, func(i, j int) bool { return c.floor[i].id < c.floor[j].id })
	d.snap.Store(c)
	return nil
}

// Snapshot 是某一时刻的只读句柄。持有它可在写入与合并持续推进时，
// 反复读取同一个完整一致的合并前（或合并后）状态。
type Snapshot struct{ s *snapshot }

// Snapshot 取当前状态的不可变只读句柄（无锁、O(1)）。
func (d *DB) View() *Snapshot { return &Snapshot{s: d.snap.Load()} }

// Get 在该快照上读取，语义与 DB.Get 相同。
func (v *Snapshot) Get(key string) (value []byte, ok bool, err error) {
	if key == "" {
		return nil, false, ErrEmptyKey
	}
	val, _, _, found := lookupView(v.s, key)
	return val, found, nil
}

// GetWithSource 在该快照上读取并返回 seq 与层（-1 为内存缓存）。
func (v *Snapshot) GetWithSource(key string) (value []byte, seq uint64, level int, ok bool, err error) {
	if key == "" {
		return nil, 0, 0, false, ErrEmptyKey
	}
	val, seq, lvl, found := lookupView(v.s, key)
	return val, seq, lvl, found, nil
}

func lookupView(s *snapshot, key string) (value []byte, seq uint64, level int, ok bool) {
	return searchSnapshot(s, key)
}

// Levels 返回各层段数（自检/观测用）。
func (d *DB) Levels() []int {
	s := d.snap.Load()
	out := make([]int, len(s.levels))
	for i, lvl := range s.levels {
		out[i] = len(lvl)
	}
	return out
}

// PendingWrites 返回尚未冻结的内存缓存条数。
func (d *DB) PendingWrites() int { return len(d.snap.Load().mem) }

// FloorCount 返回外部加载段数量。
func (d *DB) FloorCount() int { return len(d.snap.Load().floor) }

// Verify 自检不变量：层内段号严格升序、段内键严格升序且非空。
func (d *DB) Verify() error {
	s := d.snap.Load()
	checkSeg := func(seg *segment, where string) error {
		var prev string
		for _, r := range seg.records {
			if r.key == "" {
				return fmt.Errorf("%w: empty key in segment %d (%s)", ErrSegmentCorrupt, seg.id, where)
			}
			if prev != "" && r.key <= prev {
				return fmt.Errorf("verify: segment %d (%s) keys not strictly ascending", seg.id, where)
			}
			prev = r.key
		}
		return nil
	}
	for li, lvl := range s.levels {
		var prevID uint64
		for _, seg := range lvl {
			if seg.id <= prevID {
				return fmt.Errorf("verify: level %d segment ids not strictly ascending: %d after %d",
					li, seg.id, prevID)
			}
			prevID = seg.id
			if err := checkSeg(seg, fmt.Sprintf("level %d", li)); err != nil {
				return err
			}
		}
	}
	for _, seg := range s.floor {
		if err := checkSeg(seg, "floor"); err != nil {
			return err
		}
	}
	return nil
}

// EncodeSegments 按读顺序（内存冻结视图 -> 第0层..最大层 -> floor）导出段字节，
// 供“按时间顺序重放”核对：段号大的更新，逐段重放后的最终值应与 Get 一致。
func (d *DB) EncodeSegments() [][]byte {
	s := d.snap.Load()
	var out [][]byte
	if len(s.mem) > 0 {
		out = append(out, encodeSegment(freezeMem(s.mem, 0)))
	}
	for _, lvl := range s.levels {
		for _, seg := range lvl {
			out = append(out, encodeSegment(seg))
		}
	}
	for _, seg := range s.floor {
		out = append(out, encodeSegment(seg))
	}
	return out
}

// ---------------------------------------------------------------------------

func validateOptions(opts Options) error {
	if opts.MemtableSize <= 0 || opts.Fanout <= 0 || opts.MaxLevel < 0 {
		return fmt.Errorf("%w: MemtableSize=%d Fanout=%d MaxLevel=%d (want positive sizes, non-negative level)",
			ErrInvalidArgument, opts.MemtableSize, opts.Fanout, opts.MaxLevel)
	}
	return nil
}

func cloneSnapshot(s *snapshot) *snapshot {
	c := &snapshot{
		mem:       append([]record(nil), s.mem...),
		levels:    make([][]*segment, len(s.levels)),
		floor:     append([]*segment(nil), s.floor...),
		nextSegID: s.nextSegID,
		nextSeq:   s.nextSeq,
	}
	for i, lvl := range s.levels {
		c.levels[i] = append([]*segment(nil), lvl...)
	}
	return c
}

// freezeMem 把内存缓存冻结为按 key 排序的段，同键取 seq 最大（最新）一条。
func freezeMem(mem []record, id uint64) *segment {
	latest := make(map[string]record, len(mem))
	for _, r := range mem {
		if cur, ok := latest[r.key]; !ok || r.seq > cur.seq {
			latest[r.key] = r
		}
	}
	recs := make([]record, 0, len(latest))
	for _, r := range latest {
		if !r.tombstone {
			r.value = append([]byte(nil), r.value...)
		}
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].key < recs[j].key })
	return &segment{id: id, records: recs}
}

// olderWritesForKey 判断 victims 之外是否存在 key 的更早写入（段号更小）。
// 仅当不存在时，被合并墓碑才允许丢弃。
func olderWritesForKey(s *snapshot, victims []*segment, key string, tombSegID uint64) bool {
	isVictim := func(seg *segment) bool {
		for _, v := range victims {
			if v == seg {
				return true
			}
		}
		return false
	}
	check := func(list []*segment) bool {
		for _, seg := range list {
			if isVictim(seg) || seg.id >= tombSegID {
				continue
			}
			if _, found := lookupRecord(seg, key); found {
				return true
			}
		}
		return false
	}
	for _, lvl := range s.levels {
		if check(lvl) {
			return true
		}
	}
	// floor 是外部加载的基线，语义上永远早于任何合并集，不按段号比较。
	for _, seg := range s.floor {
		if isVictim(seg) {
			continue
		}
		if _, found := lookupRecord(seg, key); found {
			return true
		}
	}
	return false
}

// compactOnce 若第 level 层达到扇出阈值，则把最旧的 Fanout 个段合并为一个段。
// 一键取“段号最大的段”中的那条（墓碑与普通记录同等参与）；
// 仅当全系统不存在该键更早写入时墓碑才可丢弃。返回是否发生合并。
func compactOnce(s *snapshot, level, fanout, maxLevel int) bool {
	if len(s.levels[level]) < fanout {
		return false
	}

	victims := s.levels[level][:fanout]
	rest := s.levels[level][fanout:]

	mergedID := victims[fanout-1].id
	type pickedRec struct {
		rec   record
		segID uint64
	}
	picked := make(map[string]pickedRec)
	for _, seg := range victims {
		for _, r := range seg.records {
			cur, ok := picked[r.key]
			if !ok || seg.id > cur.segID {
				picked[r.key] = pickedRec{rec: r, segID: seg.id}
			}
		}
	}

	keys := make([]string, 0, len(picked))
	for k := range picked {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]record, 0, len(keys))
	for _, k := range keys {
		p := picked[k]
		r := p.rec
		if r.tombstone && !olderWritesForKey(s, victims, k, p.segID) {
			continue // victims 之外全系统无更早写入：墓碑无遮蔽对象，丢弃
		}
		if !r.tombstone {
			r.value = append([]byte(nil), r.value...)
		}
		out = append(out, r)
	}
	merged := &segment{id: mergedID, records: out}

	if level == maxLevel {
		// 最大层满：合并后仍留在最大层，置于剩余段之前保持段号升序。
		newLevel := make([]*segment, 0, len(rest)+1)
		newLevel = append(newLevel, merged)
		newLevel = append(newLevel, rest...)
		s.levels[level] = newLevel
		return true
	}

	s.levels[level] = append([]*segment(nil), rest...)
	s.levels[level+1] = append(s.levels[level+1], merged)
	return true
}

// settle 执行“冻结（若需要）+ 从第零层起的级联合并”。
// 每个层先在上一层合并完成后处理，保证一次写入的所有级联都在同一次发布内生效。
func settle(s *snapshot, froze bool, opts Options) {
	if froze {
		seg := freezeMem(s.mem, s.nextSegID)
		s.nextSegID++
		s.mem = nil
		s.levels[0] = append(s.levels[0], seg)
	}
	for level := 0; level <= opts.MaxLevel; level++ {
		for compactOnce(s, level, opts.Fanout, opts.MaxLevel) {
		}
	}
}
