package lwwset

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Record 保存某个元素的最新添加时间与最新删除时间。零值表示该侧尚无记录。
type Record struct {
	AddTime    int64
	RemoveTime int64
}

// present 实现并列时间戳偏向删除的成员判定：
// 元素在集合中，当且仅当存在添加时间，且删除时间严格小于添加时间。
func (rec Record) present() bool {
	return rec.AddTime > 0 && rec.RemoveTime < rec.AddTime
}

// Change 是副本本地追加的一条变更，按序号单调递增。
// AddTime、RemoveTime 中恰有一个非零，非零值恒为正。
type Change struct {
	Seq        int64
	Element    string
	AddTime    int64
	RemoveTime int64
}

// Replica 是一个 LWW 元素集合副本，所有方法可被多个 goroutine 并发调用。
type Replica struct {
	id    int64
	limit int

	mu sync.RWMutex

	// records 每个元素保留两条记录（最新添加时间、最新删除时间）。
	records map[string]Record
	// log 是本地产出变更的只增日志，序号从 1 开始。
	log []Change
	// mergePos 记录对每个来源副本已合并到的序号，用于增量合并。
	mergePos map[int64]int64
}

// NewReplica 创建一个编号为 id、记录元素数上限为 limit 的副本。
// id 必须为正整数；limit 必须为正整数（按不同元素的记录条数计数，含已删除墓碑）。
func NewReplica(id int64, limit int) (*Replica, error) {
	if id <= 0 {
		return nil, ErrInvalidReplicaID
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Replica{
		id:       id,
		limit:    limit,
		records:  make(map[string]Record),
		mergePos: make(map[int64]int64),
	}, nil
}

// ID 返回副本编号。
func (r *Replica) ID() int64 { return r.id }

func validate(element string, ts int64) error {
	if element == "" {
		return ErrEmptyElement
	}
	if ts <= 0 {
		return ErrNonPositiveTimestamp
	}
	return nil
}

// Add 在 ts 时刻添加元素。较旧的时间戳不会把已有添加记录改小；
// 即使本次没有真正改大记录，也会追加一条变更以保留完整因果历史。
func (r *Replica) Add(element string, ts int64) error {
	if err := validate(element, ts); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.records[element]; !exists && len(r.records) >= r.limit {
		return ErrLimitExceeded
	}
	rec := r.records[element]
	if ts > rec.AddTime {
		rec.AddTime = ts
	}
	r.records[element] = rec
	r.appendChange(element, ts, 0)
	return nil
}

// Remove 在 ts 时刻删除元素。删除是墓碑操作，会占用一条元素记录名额。
func (r *Replica) Remove(element string, ts int64) error {
	if err := validate(element, ts); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.records[element]; !exists && len(r.records) >= r.limit {
		return ErrLimitExceeded
	}
	rec := r.records[element]
	if ts > rec.RemoveTime {
		rec.RemoveTime = ts
	}
	r.records[element] = rec
	r.appendChange(element, 0, ts)
	return nil
}

// appendChange 追加本地变更，调用方必须持有写锁。
func (r *Replica) appendChange(element string, addTime, removeTime int64) {
	r.log = append(r.log, Change{
		Seq:        int64(len(r.log)) + 1,
		Element:    element,
		AddTime:    addTime,
		RemoveTime: removeTime,
	})
}

// Contains 判定元素当前是否在集合中。
// 规则：存在添加时间，且删除时间不存在或严格小于添加时间；两者相等视为删除。
func (r *Replica) Contains(element string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.records[element].present()
}

// Lookup 返回元素的记录副本；第二返回值表示该元素是否有任何记录（含墓碑）。
func (r *Replica) Lookup(element string) (Record, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.records[element]
	return rec, ok
}

// Elements 返回集合中当前存在的全部元素，按字典序排列以保证可复现。
func (r *Replica) Elements() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.records))
	for elem, rec := range r.records {
		if rec.present() {
			out = append(out, elem)
		}
	}
	sort.Strings(out)
	return out
}

// Records 返回全部元素记录的深拷贝快照。
func (r *Replica) Records() map[string]Record {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Record, len(r.records))
	for elem, rec := range r.records {
		out[elem] = rec
	}
	return out
}

// Seq 返回本地变更序号（已追加的本地变更条数）。
func (r *Replica) Seq() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.log))
}

// ChangesSince 返回序号大于 since 的本地变更快照，以及当前最新序号。
func (r *Replica) ChangesSince(since int64) ([]Change, int64, error) {
	if since < 0 {
		return nil, 0, ErrInvalidSince
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if since > int64(len(r.log)) {
		return nil, 0, ErrInvalidSince
	}
	out := make([]Change, len(r.log)-int(since))
	copy(out, r.log[since:])
	return out, int64(len(r.log)), nil
}

// MergePosition 返回对某来源副本已合并到的序号。
func (r *Replica) MergePosition(peerID int64) (int64, error) {
	if peerID <= 0 {
		return 0, ErrInvalidReplicaID
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mergePos[peerID], nil
}

// Checksum 返回用于自检与收敛校验的稳定摘要：
// 对全部记录（含墓碑）按元素排序后取 SHA-256，记录一致则摘要一致。
func (r *Replica) Checksum() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return checksumRecords(r.records)
}

func checksumRecords(records map[string]Record) string {
	elems := make([]string, 0, len(records))
	for elem := range records {
		elems = append(elems, elem)
	}
	sort.Strings(elems)
	var b strings.Builder
	for _, elem := range elems {
		rec := records[elem]
		b.WriteString(elem)
		b.WriteByte('=')
		b.WriteString(strconv.FormatInt(rec.AddTime, 10))
		b.WriteByte(',')
		b.WriteString(strconv.FormatInt(rec.RemoveTime, 10))
		b.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
