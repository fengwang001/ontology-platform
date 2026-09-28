package lwwset

import (
	"fmt"
	"sort"
	"sync"
)

// DefaultMaxElements 是单个副本记录元素数的默认上限。
const DefaultMaxElements = 10000

// Record 保存一个元素在某副本上的最新添加时间与最新删除时间。
// 时间戳为 0 表示对应侧尚无记录；合法时间戳恒为正整数。
type Record struct {
	AddTime int64
	DelTime int64
}

// Live 判定元素当前是否在集合中：
// 必须存在添加时间，且删除时间不存在或严格小于添加时间；两者相等视为删除。
func (r Record) Live() bool {
	return r.AddTime > 0 && r.DelTime < r.AddTime
}

// Change 是一次被接受的本地变更，用于按变更序号做增量合并。
type Change struct {
	Seq     uint64
	Element string
	Record  Record
}

// Replica 是 LWW 元素集合的一个独立副本。
type Replica struct {
	mu          sync.Mutex
	id          string
	maxElements int
	records     map[string]Record
	seq         uint64
	log         []Change
	mergePos    map[string]uint64
}

// NewReplica 创建一个编号为 id 的空副本，元素记录上限为 DefaultMaxElements。
func NewReplica(id string) (*Replica, error) {
	return NewReplicaWithLimit(id, DefaultMaxElements)
}

// NewReplicaWithLimit 创建一个指定记录元素数上限的副本。
func NewReplicaWithLimit(id string, maxElements int) (*Replica, error) {
	if id == "" {
		return nil, ErrEmptyReplicaID
	}
	if maxElements <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidMaxElements, maxElements)
	}
	return &Replica{
		id:          id,
		maxElements: maxElements,
		records:     make(map[string]Record),
		mergePos:    make(map[string]uint64),
	}, nil
}

// ID 返回副本编号。
func (r *Replica) ID() string { return r.id }

// Seq 返回本地变更序号（已接受的本地增删次数）。
func (r *Replica) Seq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// MergePosition 返回对来源副本 srcID 已合并到的变更序号。
func (r *Replica) MergePosition(srcID string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mergePos[srcID]
}

// Add 记录元素 elem 在时间戳 ts 被添加。
func (r *Replica) Add(elem string, ts int64) error {
	if err := validateElemTS(elem, ts); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.apply(elem, Record{AddTime: ts})
}

// Remove 记录元素 elem 在时间戳 ts 被删除。
func (r *Replica) Remove(elem string, ts int64) error {
	if err := validateElemTS(elem, ts); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.apply(elem, Record{DelTime: ts})
}

// apply 调用方必须持有 r.mu；全部合法性校验在任何写入之前完成。
func (r *Replica) apply(elem string, mut Record) error {
	cur, exists := r.records[elem]
	next := mergeRecord(cur, mut)
	if !exists && len(r.records) >= r.maxElements {
		return fmt.Errorf("%w: 上限 %d", ErrTooManyElements, r.maxElements)
	}
	r.records[elem] = next
	r.seq++
	r.log = append(r.log, Change{Seq: r.seq, Element: elem, Record: next})
	return nil
}

func validateElemTS(elem string, ts int64) error {
	if elem == "" {
		return ErrEmptyElement
	}
	if ts <= 0 {
		return fmt.Errorf("%w: %d", ErrNonPositiveTimestamp, ts)
	}
	return nil
}

// mergeRecord 对同一元素的两条记录按添加、删除两侧分别取较大值。
func mergeRecord(a, b Record) Record {
	return Record{
		AddTime: maxInt64(a.AddTime, b.AddTime),
		DelTime: maxInt64(a.DelTime, b.DelTime),
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Contains 判定元素当前是否在集合中。
func (r *Replica) Contains(elem string) (bool, error) {
	if elem == "" {
		return false, ErrEmptyElement
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, exists := r.records[elem]
	return exists && rec.Live(), nil
}

// Get 查询元素的两条时间记录；无任何记录时第二返回值为 false。
func (r *Replica) Get(elem string) (Record, bool, error) {
	if elem == "" {
		return Record{}, false, ErrEmptyElement
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, exists := r.records[elem]
	return rec, exists, nil
}

// Snapshot 返回全部元素记录的副本。
func (r *Replica) Snapshot() map[string]Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Record, len(r.records))
	for elem, rec := range r.records {
		out[elem] = rec
	}
	return out
}

// Elements 返回当前在集合中的全部元素（排序）。
func (r *Replica) Elements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.records))
	for elem, rec := range r.records {
		if rec.Live() {
			out = append(out, elem)
		}
	}
	sort.Strings(out)
	return out
}

// ChangesSince 返回变更序号严格大于 since 的全部本地变更。
func (r *Replica) ChangesSince(since uint64) []Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changesSinceLocked(since)
}

func (r *Replica) changesSinceLocked(since uint64) []Change {
	if since >= r.seq {
		return nil
	}
	out := make([]Change, 0, r.seq-since)
	for _, c := range r.log {
		if c.Seq > since {
			out = append(out, c)
		}
	}
	return out
}

// sourceDelta 是从来源副本一次性、原子读取的增量视图。
type sourceDelta struct {
	srcID   string
	srcSeq  uint64
	changes []Change
	records map[string]Record
}

// readSource 在来源副本的单次加锁区间内取走全部所需数据后立即解锁，
// 保证合并目标侧加锁时不同时持有来源侧锁，互逆方向合并不会互相等待。
func readSource(src *Replica, since uint64, full bool) (sourceDelta, error) {
	if src == nil {
		return sourceDelta{}, ErrNilSource
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	d := sourceDelta{srcID: src.id, srcSeq: src.seq}
	if full {
		d.records = make(map[string]Record, len(src.records))
		for elem, rec := range src.records {
			d.records[elem] = rec
		}
	} else {
		d.changes = src.changesSinceLocked(since)
	}
	return d, nil
}

// MergeFrom 将 src 整份合并进本副本，只修改本副本。
func (r *Replica) MergeFrom(src *Replica) error {
	d, err := readSource(src, 0, true)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	newCount := 0
	for elem := range d.records {
		if _, exists := r.records[elem]; !exists {
			newCount++
		}
	}
	if len(r.records)+newCount > r.maxElements {
		return fmt.Errorf("%w: 合并会使记录数达到 %d，超过上限 %d",
			ErrTooManyElements, len(r.records)+newCount, r.maxElements)
	}
	for elem, rec := range d.records {
		if cur, exists := r.records[elem]; exists {
			r.records[elem] = mergeRecord(cur, rec)
		} else {
			r.records[elem] = rec
		}
	}
	r.mergePos[d.srcID] = d.srcSeq
	return nil
}

// MergeIncremental 仅合并 src 中上次合并位置之后的变更。
func (r *Replica) MergeIncremental(src *Replica) error {
	if src == nil {
		return ErrNilSource
	}
	r.mu.Lock()
	since := r.mergePos[src.id]
	r.mu.Unlock()

	d, err := readSource(src, since, false)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// 重新读取位置：可能已有另一路针对同一来源的合并先行完成。
	if pos := r.mergePos[d.srcID]; pos > since {
		since = pos
	}
	newCount := 0
	seen := make(map[string]bool)
	for _, c := range d.changes {
		if c.Seq <= since || seen[c.Element] {
			continue
		}
		seen[c.Element] = true
		if _, exists := r.records[c.Element]; !exists {
			newCount++
		}
	}
	if len(r.records)+newCount > r.maxElements {
		return fmt.Errorf("%w: 增量合并会使记录数达到 %d，超过上限 %d",
			ErrTooManyElements, len(r.records)+newCount, r.maxElements)
	}
	for _, c := range d.changes {
		if c.Seq <= since {
			continue
		}
		cur, exists := r.records[c.Element]
		if exists {
			r.records[c.Element] = mergeRecord(cur, c.Record)
		} else {
			r.records[c.Element] = c.Record
		}
	}
	r.mergePos[d.srcID] = d.srcSeq
	return nil
}

// Check 执行内部不变量自检。
func (r *Replica) Check() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) > r.maxElements {
		return fmt.Errorf("%w: 实际记录数 %d", ErrTooManyElements, len(r.records))
	}
	if uint64(len(r.log)) != r.seq {
		return fmt.Errorf("lwwset: 自检失败，变更日志长度 %d 与序号 %d 不一致", len(r.log), r.seq)
	}
	var expectedSeq uint64
	for _, c := range r.log {
		expectedSeq++
		if c.Seq != expectedSeq {
			return fmt.Errorf("lwwset: 自检失败，变更序号不连续: 期望 %d 实际 %d", expectedSeq, c.Seq)
		}
		cur, exists := r.records[c.Element]
		if !exists {
			return fmt.Errorf("lwwset: 自检失败，变更 %d 的元素 %q 已无记录", c.Seq, c.Element)
		}
		if cur.AddTime < c.Record.AddTime || cur.DelTime < c.Record.DelTime {
			return fmt.Errorf("lwwset: 自检失败，变更 %d 的记录时间大于当前记录", c.Seq)
		}
		if c.Record.AddTime < 0 || c.Record.DelTime < 0 {
			return fmt.Errorf("lwwset: 自检失败，变更 %d 存在负时间戳", c.Seq)
		}
	}
	for elem, rec := range r.records {
		if rec.AddTime < 0 || rec.DelTime < 0 {
			return fmt.Errorf("lwwset: 自检失败，元素 %q 存在负时间戳", elem)
		}
		if rec.AddTime == 0 && rec.DelTime == 0 {
			return fmt.Errorf("lwwset: 自检失败，元素 %q 存在空记录", elem)
		}
	}
	return nil
}
